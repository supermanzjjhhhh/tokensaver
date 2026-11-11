package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"tokensaver/pkg/adapters"
	"tokensaver/pkg/core"
)

const Version = "0.1.0"

type Server struct {
	configPath string
	mu         sync.RWMutex
	cfg        Config
	profile    adapters.Profile
	upstream   *url.URL
	client     *http.Client
	stats      *StatsStore

	adminMu sync.Mutex
}

func NewServer(configPath string) (*Server, error) {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	return NewServerWithConfig(configPath, cfg)
}

func NewServerWithConfig(configPath string, cfg Config) (*Server, error) {
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	u, err := url.Parse(cfg.Upstream)
	if err != nil {
		return nil, err
	}
	profile, err := adapters.Resolve(cfg.Adapter)
	if err != nil {
		return nil, err
	}
	if !ListenIsLoopback(cfg.Listen) && !cfg.AllowRemoteManagement {
		// The proxy itself may be intentionally remote-facing, but management
		// remains disabled unless explicitly opted in. We permit a non-loopback
		// listener only when management is disabled.
		if cfg.ManagementEnabled {
			return nil, errors.New("management API on a non-loopback listener requires allow_remote_management=true")
		}
	}
	return &Server{
		configPath: configPath,
		cfg:        cfg,
		profile:    profile,
		upstream:   u,
		client:     buildClient(cfg),
		stats:      NewStatsStore(cfg.Core.Logging.MaxEntries),
	}, nil
}

func buildClient(cfg Config) *http.Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          cfg.MaxIdleConns,
		MaxIdleConnsPerHost:   cfg.MaxIdleConnsPerHost,
		IdleConnTimeout:       time.Duration(cfg.IdleConnTimeoutMS) * time.Millisecond,
		ResponseHeaderTimeout: time.Duration(cfg.ResponseHeaderTimeoutMS) * time.Millisecond,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{Transport: transport}
}

func (s *Server) Config() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *Server) Stats() Snapshot { return s.stats.Snapshot() }

func (s *Server) Handler() http.Handler { return http.HandlerFunc(s.serveHTTP) }

func (s *Server) ListenAndServe(ctx context.Context) error {
	cfg := s.Config()
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		timeout := time.Duration(s.Config().ShutdownTimeoutMS) * time.Millisecond
		shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		err := <-errCh
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/__tokensaver/") {
		s.handleAdmin(w, r)
		return
	}

	s.stats.Enter()
	defer s.stats.Leave()
	start := time.Now()
	cfg, profile, upstream, client := s.runtimeSnapshot()
	record := RequestRecord{
		Timestamp: start,
		Method:    r.Method,
		Path:      r.URL.Path,
		RequestID: requestID(r),
	}
	w.Header().Set("X-TokenSaver-Request-ID", record.RequestID)

	var (
		body         io.Reader = r.Body
		contentLen   int64     = r.ContentLength
		compResult   core.Result
		compressible = cfg.Core.Enabled && profile.IsCompressPath(r.URL.Path) && r.Method == http.MethodPost && isJSONContentType(r.Header.Get("Content-Type")) && !bypassRequested(r)
	)
	if compressible {
		prefix, overLimit, err := readWithinLimit(r.Body, cfg.MaxRequestBodyBytes)
		if err != nil {
			record.Error = "read-request-body: " + err.Error()
			record.Status = http.StatusBadRequest
			writeError(w, record.Status, "invalid_request_body", record.Error)
			record.TotalMs = msSince(start)
			s.stats.Record(record)
			return
		}
		if overLimit {
			body = io.MultiReader(bytes.NewReader(prefix), r.Body)
			contentLen = -1
			record.Fallback = true
			record.Error = "request-body-over-limit; passthrough"
		} else {
			raw := prefix
			model, stream := inspectRequest(raw)
			record.Model = model
			record.Protocol = profile.ProtocolForPath(r.URL.Path)
			record.BytesIn = len(raw)
			compResult = core.CompressRequest(raw, cfg.Core, core.RequestMeta{
				Protocol:     record.Protocol,
				Model:        model,
				SourceFormat: record.Protocol,
				RequestID:    record.RequestID,
				Stream:       stream,
			})
			body = bytes.NewReader(compResult.Body)
			contentLen = int64(len(compResult.Body))
			record.CompressionApplied = compResult.Applied
			record.Fallback = compResult.Stats.Fallback
			record.OriginalTokens = compResult.Stats.OriginalTokens
			record.CompressedTokens = compResult.Stats.CompressedTokens
			record.SavingsPercent = compResult.Stats.SavingsPercent
			record.CompressionMs = compResult.Stats.CompressionMs
			record.Engines = compResult.Stats.Engines
			if compResult.Stats.SkippedReason != "" && compResult.Stats.Fallback {
				record.Error = compResult.Stats.SkippedReason
			}
		}
	}

	status, bytesOut, upstreamMs, err := s.forward(w, r, body, contentLen, &record, upstream, client)
	record.Status = status
	record.BytesOut = bytesOut
	record.UpstreamMs = upstreamMs
	record.TotalMs = msSince(start)
	if err != nil {
		record.Error = err.Error()
	}
	if record.CompressionApplied {
		w.Header().Set("X-TokenSaver-Applied", "true")
		w.Header().Set("X-TokenSaver-Savings-Percent", fmt.Sprintf("%.2f", record.SavingsPercent))
	} else {
		w.Header().Set("X-TokenSaver-Applied", "false")
	}
	s.stats.Record(record)
}

func (s *Server) runtimeSnapshot() (Config, adapters.Profile, *url.URL, *http.Client) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg := s.cfg
	cfg.Core = s.cfg.Core.Clone()
	upstream := *s.upstream
	return cfg, s.profile, &upstream, s.client
}

func (s *Server) forward(w http.ResponseWriter, r *http.Request, body io.Reader, contentLen int64, record *RequestRecord, upstream *url.URL, client *http.Client) (int, int64, float64, error) {
	upstreamStart := time.Now()
	target := targetURL(upstream, r.URL)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	if notifier, ok := w.(http.CloseNotifier); ok {
		closed := notifier.CloseNotify()
		go func() {
			select {
			case <-closed:
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, target.String(), body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "proxy_request_error", "could not create upstream request")
		return http.StatusBadRequest, 0, msSince(upstreamStart), err
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		req.Body = nil
		req.ContentLength = 0
	} else if contentLen >= 0 {
		req.ContentLength = contentLen
	}
	cloneHeaders(req.Header, r.Header)
	req.Host = upstream.Host
	req.Header.Del("Content-Length")
	req.Header.Del("X-TokenSaver-Bypass")
	req.Header.Del("X-TokenSaver-Compressed")

	resp, err := client.Do(req)
	if err != nil {
		upstreamMs := msSince(upstreamStart)
		if r.Context().Err() != nil {
			return 499, 0, upstreamMs, fmt.Errorf("client-canceled")
		}
		writeError(w, http.StatusBadGateway, "upstream_error", "upstream request failed")
		return http.StatusBadGateway, 0, upstreamMs, err
	}
	defer resp.Body.Close()
	copyHeaders(w.Header(), resp.Header)
	// Compression response headers are added before WriteHeader so they reach the client.
	if record.CompressionApplied {
		w.Header().Set("X-TokenSaver-Applied", "true")
		w.Header().Set("X-TokenSaver-Savings-Percent", fmt.Sprintf("%.2f", record.SavingsPercent))
	} else {
		w.Header().Set("X-TokenSaver-Applied", "false")
	}
	w.WriteHeader(resp.StatusCode)
	n, copyErr := copyResponse(w, resp)
	upstreamMs := msSince(upstreamStart)
	if copyErr != nil && r.Context().Err() == nil {
		return resp.StatusCode, n, upstreamMs, fmt.Errorf("copy-response: %w", copyErr)
	}
	return resp.StatusCode, n, upstreamMs, nil
}

func copyResponse(w http.ResponseWriter, resp *http.Response) (int64, error) {
	flusher, canFlush := w.(http.Flusher)
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") && canFlush {
		buf := make([]byte, 32*1024)
		var total int64
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				nw, werr := w.Write(buf[:n])
				total += int64(nw)
				if werr != nil {
					return total, werr
				}
				flusher.Flush()
			}
			if err != nil {
				if err == io.EOF {
					return total, nil
				}
				return total, err
			}
		}
	}
	return io.Copy(w, resp.Body)
}

func targetURL(upstream *url.URL, in *url.URL) *url.URL {
	u := *upstream
	basePath := strings.TrimRight(u.Path, "/")
	inPath := in.Path
	if inPath == "" {
		inPath = "/"
	}
	u.Path = basePath + inPath
	u.RawPath = ""
	u.RawQuery = in.RawQuery
	u.Fragment = ""
	return &u
}

func readWithinLimit(r io.Reader, limit int64) ([]byte, bool, error) {
	if limit < 1 {
		return nil, false, errors.New("invalid body limit")
	}
	var buf bytes.Buffer
	if _, err := io.CopyN(&buf, r, limit+1); err != nil && err != io.EOF {
		return nil, false, err
	}
	if int64(buf.Len()) > limit {
		return buf.Bytes(), true, nil
	}
	return buf.Bytes(), false, nil
}

func inspectRequest(body []byte) (string, bool) {
	var meta struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	_ = json.Unmarshal(body, &meta)
	return meta.Model, meta.Stream
}

func isJSONContentType(v string) bool {
	if v == "" {
		return false
	}
	mt, _, err := mime.ParseMediaType(v)
	if err != nil {
		return false
	}
	return mt == "application/json" || strings.HasSuffix(mt, "+json")
}

func bypassRequested(r *http.Request) bool {
	v := strings.TrimSpace(strings.ToLower(r.Header.Get("X-TokenSaver-Bypass")))
	return v == "1" || v == "true" || r.Header.Get("X-TokenSaver-Compressed") == "1"
}

func requestID(r *http.Request) string {
	if id := strings.TrimSpace(r.Header.Get("X-Request-ID")); id != "" {
		return id
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return "ts-" + hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("ts-%d", time.Now().UnixNano())
}

func cloneHeaders(dst, src http.Header) {
	for k, vals := range src {
		if isHopByHop(k) || strings.HasPrefix(strings.ToLower(k), "x-tokensaver-") {
			continue
		}
		for _, v := range vals {
			dst.Add(k, v)
		}
	}
}

func copyHeaders(dst, src http.Header) {
	for k, vals := range src {
		if isHopByHop(k) {
			continue
		}
		for _, v := range vals {
			dst.Add(k, v)
		}
	}
}

func isHopByHop(k string) bool {
	switch strings.ToLower(k) {
	case "connection", "proxy-connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"type":    code,
			"message": message,
		},
	})
}

func msSince(t time.Time) float64 { return float64(time.Since(t).Microseconds()) / 1000 }
