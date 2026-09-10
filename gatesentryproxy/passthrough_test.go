package gatesentryproxy

import "testing"

func TestPassthroughManagementHost(t *testing.T) {
	if !PassthroughManagementHost("api.x.ai") {
		t.Fatal("api.x.ai")
	}
	if !PassthroughManagementHost("API.OpenAI.com:443") {
		t.Fatal("api.openai.com:443")
	}
	if PassthroughManagementHost("example.com") {
		t.Fatal("example.com must not passthrough")
	}
	if PassthroughManagementHost("192.0.2.10") {
		t.Fatal("documentation IP must not passthrough")
	}
}
