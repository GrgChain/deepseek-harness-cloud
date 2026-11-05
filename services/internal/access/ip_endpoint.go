package access

import (
	"context"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
)

type Endpoint struct {
	CanonicalOrigin string `json:"canonical_origin"`
	IPOrigin        string `json:"ip_origin,omitempty"`
	IPBasePath      string `json:"ip_base_path,omitempty"`
}

func (s *Service) Endpoint(ctx context.Context, p identity.Principal, id string) (Endpoint, error) {
	target, err := s.store.Prepare(ctx, p, id)
	if err != nil {
		return Endpoint{}, err
	}
	if s.ownership == nil {
		return Endpoint{}, unavailable()
	}
	if err = s.ownership.Check(ctx, target); err != nil {
		return Endpoint{}, err
	}
	out := Endpoint{CanonicalOrigin: s.store.rules.URL(target.Host)}
	if s.ipAccess == nil {
		return out, nil
	}
	out.IPOrigin = s.ipAccess.Origin(s.ipAccess.PortalPort)
	out.IPBasePath = ipWorkspacePath(id)
	return out, nil
}
