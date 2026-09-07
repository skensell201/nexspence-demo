package repository_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/repository"
)

func TestUniqueViolationError_NamesTheFieldAndMatchesTheSentinel(t *testing.T) {
	err := error(&repository.UniqueViolationError{Field: "email"})
	if got := err.Error(); got != "email already exists" {
		t.Fatalf("Error() = %q", got)
	}
	if !errors.Is(err, repository.ErrAlreadyExists) {
		t.Fatal("a UniqueViolationError must satisfy errors.Is(err, ErrAlreadyExists)")
	}
	if errors.Is(err, repository.ErrNotFound) {
		t.Fatal("it must not match an unrelated sentinel")
	}
	// Callers wrap it on the way up; the sentinel has to survive that.
	if !errors.Is(fmt.Errorf("create user: %w", err), repository.ErrAlreadyExists) {
		t.Fatal("wrapping lost the sentinel")
	}
}

func TestRequestNotPendingError_CarriesTheStatus(t *testing.T) {
	err := error(&repository.RequestNotPendingError{Status: "approved"})
	if got := err.Error(); got != "request is not pending (status: approved)" {
		t.Fatalf("Error() = %q", got)
	}
	var target *repository.RequestNotPendingError
	if !errors.As(fmt.Errorf("promote: %w", err), &target) || target.Status != "approved" {
		t.Fatal("errors.As must recover the status a concurrent reviewer set")
	}
}
