package access

import (
	"context"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type Ownership interface {
	Check(context.Context, Target) error
}
type NativeVerifier interface {
	Verify(context.Context, Authorization) error
}
type Service struct {
	store     *Store
	ownership Ownership
	native    NativeVerifier
}

func NewService(store *Store, ownership Ownership, native NativeVerifier) *Service {
	return &Service{store, ownership, native}
}

func (s *Service) ReplaceChallenge(ctx context.Context, host, previous string) (Challenge, string, error) {
	return s.store.ReplaceChallenge(ctx, host, previous)
}

func (s *Service) Issue(ctx context.Context, p identity.Principal, id, challengeID, trace string) (Handoff, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if s.ownership == nil {
		return Handoff{}, unavailable()
	}
	target, err := s.store.Prepare(ctx, p, id)
	if err != nil {
		return Handoff{}, err
	}
	if err = s.ownership.Check(ctx, target); err != nil {
		return Handoff{}, err
	}
	return s.store.Issue(ctx, p, target, challengeID, trace)
}

func (s *Service) Authorize(ctx context.Context, host, secret string) (Authorization, error) {
	return s.CheckSession(ctx, host, secret, true)
}

// Connection checks do not extend the Portal idle deadline. Only real browser
// activity may refresh it; ping/pong and server timers are not activity.
func (s *Service) CheckSession(ctx context.Context, host, secret string, activity bool) (Authorization, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if s.ownership == nil {
		return Authorization{}, unavailable()
	}
	candidate, err := s.store.Authorize(ctx, host, secret, nil, false)
	if err != nil {
		return Authorization{}, err
	}
	defer candidate.Destroy()
	if err = s.ownership.Check(ctx, candidate.Target); err != nil {
		return Authorization{}, err
	}
	return s.store.Authorize(ctx, host, secret, &candidate.Target, activity)
}

func (s *Service) Redeem(ctx context.Context, host, ticket, challengeID, browser, trace string) (secret string, result Authorization, err error) {
	if s.ownership == nil || s.native == nil {
		return "", result, unavailable()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	claimed, stop := context.WithTimeout(ctx, 2*time.Second)
	target, err := s.store.PrepareHandoff(claimed, host, ticket, challengeID, browser)
	if err == nil {
		err = s.ownership.Check(claimed, target)
	}
	var consumed Authorization
	if err == nil {
		secret, consumed, err = s.store.Consume(claimed, host, ticket, challengeID, browser, target, trace)
	}
	stop()
	if err != nil {
		return "", result, err
	}
	// The newly minted secret is withheld until native verification and another
	// live authorization. Failure never restores the consumed ticket/challenge.
	success := false
	defer func() {
		if !success {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			_ = s.store.Revoke(cleanup, consumed, trace)
		}
	}()
	probe, stop := context.WithTimeout(ctx, 5*time.Second)
	native, err := s.store.Authorize(probe, host, secret, &target, false)
	if err == nil {
		err = s.native.Verify(probe, native)
	}
	native.Destroy()
	stop()
	if err != nil {
		return "", Authorization{}, webapi.Fail(502, "NATIVE_AUTH_FAILED", "工作空间原生会话暂不可用，请从控制台重新进入")
	}
	result, err = s.Authorize(ctx, host, secret)
	if err != nil {
		return "", Authorization{}, err
	}
	audit, stop := context.WithTimeout(ctx, time.Second)
	err = s.store.Entered(audit, result, trace)
	stop()
	if err != nil {
		result.Destroy()
		return "", Authorization{}, err
	}
	success = true
	return secret, result, nil
}
