package modelgateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"strconv"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/modelendpoint"
)

type Options struct {
	SchemaVersion      int      `json:"schema_version"`
	Address            string   `json:"address"`
	CertificateFile    string   `json:"certificate_file"`
	CertificateKeyFile string   `json:"certificate_key_file"`
	ControlKeyFile     string   `json:"control_key_file"`
	DataKeyFile        string   `json:"data_key_file"`
	DataKeyringFile    string   `json:"data_keyring_file,omitempty"`
	DataKeyID          string   `json:"data_key_id"`
	Environment        string   `json:"environment"`
	CustomAPIBases     []string `json:"custom_api_bases,omitempty"`
}

func LoadOptions(path string) (Options, error) {
	var o Options
	raw, err := config.ReadPrivateFile(path)
	if err != nil {
		return o, errProtocol
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&o) != nil || d.Decode(new(any)) != io.EOF || !modelendpoint.ValidBases(o.CustomAPIBases) || o.SchemaVersion != 1 || o.Environment == "" || o.DataKeyID == "" {
		return o, errProtocol
	}
	host, port, err := net.SplitHostPort(o.Address)
	n, e := strconv.Atoi(port)
	ip := net.ParseIP(host)
	if err != nil || e != nil || n <= 10000 || n > 65535 || ip == nil || (!ip.IsPrivate() && !ip.IsLoopback()) || ip.IsUnspecified() {
		return o, errProtocol
	}
	for _, p := range []string{o.CertificateFile, o.CertificateKeyFile, o.ControlKeyFile, o.DataKeyFile} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return o, errProtocol
		}
	}
	if o.DataKeyringFile != "" && (!filepath.IsAbs(o.DataKeyringFile) || filepath.Clean(o.DataKeyringFile) != o.DataKeyringFile) {
		return o, errProtocol
	}
	return o, nil
}
