package wire

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestServerAuthorizeNilAllowsAll(t *testing.T) {
	// With no Authorizer set, every session is allowed.
	srv := &Server{}
	s := &Session{Service: ServiceUploadPack, Repo: "org/repo.git"}
	if err := srv.Authorize(context.Background(), s); err != nil {
		t.Fatalf("Authorize with nil Authorizer = %v, want nil", err)
	}
}

func TestServerAuthorizeAllows(t *testing.T) {
	var seen *Session
	srv := &Server{Authorizer: AuthorizeFunc(func(ctx context.Context, s *Session) error {
		seen = s
		return nil
	})}
	s := &Session{Service: ServiceUploadPack, Repo: "org/repo.git", User: "alice"}
	if err := srv.Authorize(context.Background(), s); err != nil {
		t.Fatalf("Authorize = %v, want nil", err)
	}
	if seen != s {
		t.Fatal("Authorizer did not receive the session")
	}
}

func TestServerAuthorizeRejects(t *testing.T) {
	srv := &Server{Authorizer: AuthorizeFunc(func(ctx context.Context, s *Session) error {
		return ErrUnauthorized
	})}
	s := &Session{Service: ServiceReceivePack, Repo: "org/repo.git"}
	err := srv.Authorize(context.Background(), s)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Authorize = %v, want ErrUnauthorized", err)
	}
}

func TestServerAuthorizeWrappedUnauthorized(t *testing.T) {
	// An authorizer may wrap ErrUnauthorized with context; errors.Is must still
	// recognize it.
	srv := &Server{Authorizer: AuthorizeFunc(func(ctx context.Context, s *Session) error {
		return fmt.Errorf("user %q may not push to %q: %w", s.User, s.Repo, ErrUnauthorized)
	})}
	s := &Session{Service: ServiceReceivePack, Repo: "org/repo.git", User: "bob"}
	err := srv.Authorize(context.Background(), s)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Authorize = %v, want wrapped ErrUnauthorized", err)
	}
}

func TestAuthorizeFuncImplementsAuthorizer(t *testing.T) {
	var a Authorizer = AuthorizeFunc(func(ctx context.Context, s *Session) error { return nil })
	if err := a.Authorize(context.Background(), &Session{}); err != nil {
		t.Fatalf("AuthorizeFunc.Authorize = %v", err)
	}
}

func TestClientErrorMessageUnauthorized(t *testing.T) {
	if got := ClientErrorMessage(ErrUnauthorized); got != "unauthorized" {
		t.Fatalf("ClientErrorMessage(ErrUnauthorized) = %q, want %q", got, "unauthorized")
	}
	if got := ClientErrorMessage(fmt.Errorf("denied: %w", ErrUnauthorized)); got != "unauthorized" {
		t.Fatalf("ClientErrorMessage(wrapped) = %q, want %q", got, "unauthorized")
	}
	// A non-sentinel error must not leak its detail.
	if got := ClientErrorMessage(errors.New("secret policy detail")); got != "internal server error" {
		t.Fatalf("ClientErrorMessage(other) = %q, want generic", got)
	}
}
