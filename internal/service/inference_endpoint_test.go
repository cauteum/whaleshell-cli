package service

import (
	"context"
	"testing"

	"github.com/cautem/cauteum-core/defaults"
	"github.com/cautem/cauteum-sdk/go/cauteum"
)

type providerEndpointStub struct{ record cauteum.ProviderRecord }

func (s providerEndpointStub) GetProvider(context.Context, string) (cauteum.ProviderRecord, error) {
	return s.record, nil
}
func TestInferenceUpstreamConfiguration(t *testing.T) {
	cases := []struct {
		name   string
		record cauteum.ProviderRecord
		want   string
	}{
		{"deepinfra", cauteum.ProviderRecord{Type: "deepinfra"}, defaults.InferenceDeepInfra},
		{"unknown", cauteum.ProviderRecord{Type: "custom"}, ""},
		{"override", cauteum.ProviderRecord{Type: "custom", Config: cauteum.ProviderConfig{"base_url": "https://models.example/v1"}}, "https://models.example/v1"},
		{"invalid", cauteum.ProviderRecord{Type: "openai", Config: cauteum.ProviderConfig{"base_url": "file:///credentials"}}, ""},
		{"userinfo", cauteum.ProviderRecord{Type: "openai", Config: cauteum.ProviderConfig{"base_url": "https://secret@models.example"}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := inferenceUpstreamForType("test", providerEndpointStub{tc.record}, context.Background())
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
