package runtimeapproval

import (
	"context"
	"testing"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func TestRetainedApprovalBindsHistoricalImageAndActualCoderVersion(t *testing.T) {
	for _, name := range []string{"valid inactive target", "changed adapter", "changed policy", "changed helper", "changed archive", "archived target", "unrelated active version", "changed manifest", "unknown file"} {
		t.Run(name, func(t *testing.T) {
			f, c, sources, image := verificationFixture()
			approved, err := Verify(context.Background(), f, c, f.user.ID, "/private/runtime", sources, image)
			if err != nil {
				t.Fatal(err)
			}
			current := webapi.ID()
			f.template.ActiveVersion = current
			// A retained image is bound to its previously approved digests. Local
			// runtime sources may now belong to a newer compatible release.
			delete(sources, "bootstrap.mjs")
			sources["contract.mjs"] = []byte("new reviewed release")
			switch name {
			case "changed adapter":
				image.Files["bootstrap.mjs"] = SHA([]byte("changed"))
			case "changed policy":
				image.Files["contract.mjs"] = SHA([]byte("changed"))
			case "changed helper":
				image.Files["storage-helper.py"] = SHA([]byte("changed"))
			case "changed archive":
				f.files["main.tf"] = []byte("changed")
			case "archived target":
				f.version.Archived = true
			case "unrelated active version":
				f.template.ActiveVersion = webapi.ID()
			case "changed manifest":
				approved.AdapterDigest = approved.Image
			case "unknown file":
				image.Files["unknown"] = SHA([]byte("unknown"))
			}
			if err = VerifyRetained(context.Background(), f, c, f.user.ID, "/private/runtime", current, sources, image, approved); (err == nil) != (name == "valid inactive target") {
				t.Fatalf("retained verification accepted=%v: %v", err == nil, err)
			}
		})
	}
}
