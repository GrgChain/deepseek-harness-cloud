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

func TestGraphitiURL(t *testing.T) {
	for _, value := range []string{"bolt+s://neo4j.example.com:7687", "bolt://neo4j.internal:7687/"} {
		if _, err := graphitiURL(value, true); err != nil {
			t.Fatal(value, err)
		}
	}
	for _, value := range []string{"https://neo4j.example", "bolt+ssc://neo4j.example", "bolt+s://u:p@neo4j.example", "bolt+s://neo4j.example/path", "bolt+s://neo4j.example?", "bolt+s://neo4j.example:abc"} {
		if _, err := graphitiURL(value, true); err == nil {
			t.Fatal("accepted invalid service", value)
		}
	}
	if _, err := graphitiURL("bolt://neo4j.internal:7687", false); err == nil {
		t.Fatal("ignored unencrypted transport policy")
	}
}
