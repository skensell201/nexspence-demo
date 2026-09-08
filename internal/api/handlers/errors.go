package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/service"
)

func isNotFound(err error) bool {
	return errors.Is(err, service.ErrNotFound)
}

// isAlreadyExists covers both sentinels: a service that phrases the conflict
// itself, and a repository conflict a service passes straight through.
func isAlreadyExists(err error) bool {
	return errors.Is(err, service.ErrAlreadyExists) || errors.Is(err, repository.ErrAlreadyExists)
}

// conflictOnDuplicateName answers 409 when err is a duplicate-name conflict and
// reports whether it did, so a handler can `if conflictOnDuplicateName(...) {
// return }` ahead of its own error mapping. The message is fixed rather than
// taken from err: the driver's own text names the constraint and the table
// behind it, which is not the caller's business.
func conflictOnDuplicateName(c *gin.Context, err error) bool {
	if !isAlreadyExists(err) {
		return false
	}
	c.JSON(http.StatusConflict, gin.H{"error": "name already exists"})
	return true
}

func isInvalidInput(err error) bool {
	return errors.Is(err, service.ErrInvalidInput)
}
