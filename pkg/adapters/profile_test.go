package adapters

import "testing"

func TestCountTokensIsNeverCompressed(t *testing.T) {
	for _, p := range []Profile{CPAProfile, Agent2APIProfile, GenericProfile} {
		if p.IsCompressPath("/v1/messages/count_tokens") {
			t.Fatalf("%s profile must pass count_tokens through untouched", p.Name)
		}
	}
}
