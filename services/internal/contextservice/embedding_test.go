package contextservice

import "testing"

func TestEmbeddingValidation(t *testing.T) {
	valid := EmbeddingConfig{Provider: "openai_compatible", Model: "my-model", APIBase: "https://example.com/v1/", Dimensions: 1536, APIKey: "secret"}
	got, err := validateEmbedding(valid, false)
	if err != nil || got.APIBase != "https://example.com/v1" {
		t.Fatal(got, err)
	}
	if got, err := validateEmbedding(EmbeddingConfig{Provider: "local"}, false); err != nil || got != defaultEmbedding() {
		t.Fatal(got, err)
	}
	for _, change := range []func(*EmbeddingConfig){func(e *EmbeddingConfig) { e.Provider = "other" }, func(e *EmbeddingConfig) { e.Model = "" }, func(e *EmbeddingConfig) { e.Dimensions = 0 }, func(e *EmbeddingConfig) { e.Dimensions = 32769 }, func(e *EmbeddingConfig) { e.APIBase = "https://user:secret@example.com/v1" }, func(e *EmbeddingConfig) { e.APIBase = "https://example.com/v1?key=secret" }, func(e *EmbeddingConfig) { e.APIBase = "http://example.com/v1" }, func(e *EmbeddingConfig) { e.APIKey = "secret\n" }} {
		bad := valid
		change(&bad)
		if _, err := validateEmbedding(bad, false); err == nil {
			t.Fatal("accepted invalid embedding")
		}
	}
	if _, err := validateEmbedding(EmbeddingConfig{Provider: "local", Model: "not-installed"}, true); err == nil {
		t.Fatal("accepted unavailable local model")
	}
}
