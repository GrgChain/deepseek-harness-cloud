package contextservice

import "testing"

func TestMilvusURL(t *testing.T) {
	for _, value := range []string{"https://milvus.example.com", "https://milvus.example.com:19530", "http://milvus.internal:19530/"} {
		if _, err := milvusURL(value, true); err != nil {
			t.Fatal(value, err)
		}
	}
	for _, value := range []string{"file:///data/milvus", "https://root:secret@milvus.example", "https://milvus.example/db", "https://milvus.example/?token=secret", "https://milvus.example/#secret", "https://milvus.example?", "https://milvus.example:abc"} {
		if _, err := milvusURL(value, true); err == nil {
			t.Fatal("accepted invalid service", value)
		}
	}
	if _, err := milvusURL("http://milvus.internal:19530", false); err == nil {
		t.Fatal("ignored HTTP policy")
	}
}
