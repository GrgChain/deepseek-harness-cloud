package operations

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

func TestEncryptedArchiveStreamingIntegrity(t *testing.T) {
	key := bytes.Repeat([]byte{11}, 32)
	body := bytes.Repeat([]byte("private backup payload"), 10000)
	manifest := BackupManifest{SchemaVersion: 1, Kind: "postgres", OrganizationID: "00000000-0000-4000-8000-000000000001", CreatedAt: time.Now().UTC(), Database: "hc_test_operations"}
	var encrypted bytes.Buffer
	proof, err := SealArchive(&encrypted, bytes.NewReader(body), key, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted.Bytes(), []byte("private backup payload")) || bytes.Contains(encrypted.Bytes(), []byte(manifest.Database)) {
		t.Fatal("backup exposed plaintext")
	}
	var restored bytes.Buffer
	got, verified, err := OpenArchive(&restored, bytes.NewReader(encrypted.Bytes()), key)
	if err != nil || got.Database != manifest.Database || proof != verified || !bytes.Equal(body, restored.Bytes()) {
		t.Fatal("backup failed integrity round trip", err)
	}
	wrong := bytes.Repeat([]byte{12}, 32)
	if _, _, err := OpenArchive(&bytes.Buffer{}, bytes.NewReader(encrypted.Bytes()), wrong); err == nil {
		t.Fatal("wrong key accepted")
	}
	for _, at := range []int{0, 8, 19, len(encrypted.Bytes()) / 2, len(encrypted.Bytes()) - 1} {
		changed := bytes.Clone(encrypted.Bytes())
		changed[at] ^= 1
		if _, _, err := OpenArchive(&bytes.Buffer{}, bytes.NewReader(changed), key); err == nil {
			t.Fatalf("tampered archive accepted at %d", at)
		}
	}
	for _, n := range []int{0, 8, 19, 100, len(encrypted.Bytes()) - 1} {
		if _, _, err := OpenArchive(&bytes.Buffer{}, bytes.NewReader(encrypted.Bytes()[:n]), key); err == nil {
			t.Fatal("truncated archive accepted")
		}
	}
	trailing := append(bytes.Clone(encrypted.Bytes()), 0)
	if _, _, err := OpenArchive(&bytes.Buffer{}, bytes.NewReader(trailing), key); err == nil {
		t.Fatal("trailing bytes accepted")
	}
	oversize := bytes.Clone(encrypted.Bytes())
	binary.BigEndian.PutUint32(oversize[40:44], 1<<31)
	if _, _, err := OpenArchive(&bytes.Buffer{}, bytes.NewReader(oversize), key); err == nil {
		t.Fatal("unbounded frame accepted")
	}
}
