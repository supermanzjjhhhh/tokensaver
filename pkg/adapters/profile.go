package adapters

import (
	"fmt"
	"net/http"
	"strings"
)

type Profile struct {
	Name             string   `json:"name"`
	CompressPaths    []string `json:"compress_paths"`
	PassThroughPaths []string `json:"pass_through_paths"`
	PreserveHeaders  []string `json:"preserve_headers"`
}

var (
	CPAProfile = Profile{
		Name: "cpa",
		CompressPaths: []string{
			"/v1/chat/completions",
			"/v1/responses",
			"/v1/messages",
		},
		PassThroughPaths: []string{"/v1/models", "/v1/messages/count_tokens", "/v1/images/generations", "/v1/images/edits", "/health"},
		PreserveHeaders:  []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key", "OpenAI-Beta", "Anthropic-Beta", "Anthropic-Version"},
	}
	Agent2APIProfile = Profile{
		Name: "agent2api",
		CompressPaths: []string{
			"/v1/chat/completions",
			"/v1/responses",
			"/v1/messages",
		},
		PassThroughPaths: []string{"/v1/models", "/v1/messages/count_tokens", "/v1/images/generations", "/v1/images/edits", "/health"},
		PreserveHeaders:  []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key", "OpenAI-Beta", "Anthropic-Beta", "Anthropic-Version"},
	}
	GenericProfile = Profile{
		Name:             "generic",
		CompressPaths:    CPAProfile.CompressPaths,
		PassThroughPaths: CPAProfile.PassThroughPaths,
		PreserveHeaders:  CPAProfile.PreserveHeaders,
	}
)

func Resolve(name string) (Profile, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "generic":
		return GenericProfile, nil
	case "cpa", "cliproxyapi":
		return CPAProfile, nil
	case "agent2api", "agent2-api":
		return Agent2APIProfile, nil
	default:
		return Profile{}, fmt.Errorf("unsupported adapter profile %q", name)
	}
}

func (p Profile) IsCompressPath(path string) bool {
	for _, candidate := range p.CompressPaths {
		if path == candidate {
			return true
		}
	}
	return false
}

func (p Profile) ProtocolForPath(path string) string {
	switch path {
	case "/v1/responses":
		return "responses"
	case "/v1/messages", "/v1/messages/count_tokens":
		return "anthropic"
	case "/v1/chat/completions":
		return "chat"
	default:
		return ""
	}
}

func (p Profile) PreserveHeader(name string) bool {
	for _, h := range p.PreserveHeaders {
		if http.CanonicalHeaderKey(h) == http.CanonicalHeaderKey(name) {
			return true
		}
	}
	return false
}
