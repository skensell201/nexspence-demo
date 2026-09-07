package auth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/nexspence-oss/nexspence/internal/auth"
)

func newSvc() *auth.Service {
	// bcryptCost=4 is minimum — fast in tests, not for production
	return auth.NewService("testsecret-long-enough-32bytes!!", 1, 4)
}

func TestGenerateToken_ValidateClaims(t *testing.T) {
	s := newSvc()
	token, err := s.GenerateToken("uid-1", "alice", []string{"admin", "deployer"})
	require.NoError(t, err)
	require.NotEmpty(t, token)

	claims, err := s.ValidateToken(token)
	require.NoError(t, err)
	assert.Equal(t, "uid-1", claims.UserID)
	assert.Equal(t, "alice", claims.Username)
	assert.ElementsMatch(t, []string{"admin", "deployer"}, claims.Roles)
}

func TestGenerateToken_SetsIssuedAt(t *testing.T) {
	s := newSvc()
	token, err := s.GenerateToken("uid-iat", "iatuser", nil)
	require.NoError(t, err)
	claims, err := s.ValidateToken(token)
	require.NoError(t, err)
	require.NotNil(t, claims.IssuedAt, "GenerateToken must set the iat claim")
}

func TestGenerateTokenWithMethod_SetsIssuedAt(t *testing.T) {
	s := newSvc()
	token, err := s.GenerateTokenWithMethod("uid-iat2", "iatuser2", nil, "oidc")
	require.NoError(t, err)
	claims, err := s.ValidateToken(token)
	require.NoError(t, err)
	require.NotNil(t, claims.IssuedAt, "GenerateTokenWithMethod must set the iat claim")
}

func TestGenerateToken_NoRoles(t *testing.T) {
	s := newSvc()
	token, err := s.GenerateToken("uid-2", "bob", nil)
	require.NoError(t, err)

	claims, err := s.ValidateToken(token)
	require.NoError(t, err)
	assert.Equal(t, "bob", claims.Username)
}

func TestValidateToken_Malformed(t *testing.T) {
	s := newSvc()
	_, err := s.ValidateToken("this.is.not.a.jwt")
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestValidateToken_EmptyString(t *testing.T) {
	s := newSvc()
	_, err := s.ValidateToken("")
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestValidateToken_WrongSecret(t *testing.T) {
	s1 := auth.NewService("secret-alpha-padding-32bytes!!", 1, 4)
	s2 := auth.NewService("secret-beta--padding-32bytes!!", 1, 4)

	token, err := s1.GenerateToken("uid-3", "carol", []string{"viewer"})
	require.NoError(t, err)

	_, err = s2.ValidateToken(token)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestHashPassword_CheckPassword(t *testing.T) {
	s := newSvc()
	hash, err := s.HashPassword("super-secret-pass")
	require.NoError(t, err)
	assert.NotEmpty(t, hash)
	// Hashes are different even for the same input (bcrypt salt)
	hash2, _ := s.HashPassword("super-secret-pass")
	assert.NotEqual(t, hash, hash2)

	require.NoError(t, s.CheckPassword(hash, "super-secret-pass"))
}

func TestCheckPassword_WrongPassword(t *testing.T) {
	s := newSvc()
	hash, _ := s.HashPassword("correct-horse")
	err := s.CheckPassword(hash, "wrong-guess")
	assert.ErrorIs(t, err, auth.ErrBadPassword)
}

func TestCheckPassword_EmptyPassword(t *testing.T) {
	s := newSvc()
	hash, _ := s.HashPassword("notempty")
	err := s.CheckPassword(hash, "")
	assert.ErrorIs(t, err, auth.ErrBadPassword)
}

func TestGenerateTokenWithMethod_SetsAuthMethod(t *testing.T) {
	s := newSvc()
	token, err := s.GenerateTokenWithMethod("uid-9", "dave", []string{"viewer"}, "oidc")
	require.NoError(t, err)
	claims, err := s.ValidateToken(token)
	require.NoError(t, err)
	assert.Equal(t, "dave", claims.Username)
	assert.Equal(t, "oidc", claims.AuthMethod)
}

func TestGenerateToken_NoAuthMethod(t *testing.T) {
	s := newSvc()
	token, err := s.GenerateToken("uid-10", "erin", nil)
	require.NoError(t, err)
	claims, err := s.ValidateToken(token)
	require.NoError(t, err)
	assert.Empty(t, claims.AuthMethod)
}

func TestValidateToken_Expired(t *testing.T) {
	s := auth.NewService("testsecret-long-enough-32bytes!!", -1, 4) // expires 1h in the past
	token, err := s.GenerateToken("uid-11", "frank", nil)
	require.NoError(t, err)
	_, err = s.ValidateToken(token)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestValidateToken_NonHMACAlg_Rejected(t *testing.T) {
	// Sign with RS256 (non-HMAC) to trigger the alg-confusion guard.
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"sub": "mallory",
		"iss": "nexspence",
	})
	signed, err := tok.SignedString(privateKey)
	require.NoError(t, err)
	s := newSvc()
	_, err = s.ValidateToken(signed)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestValidateToken_NoneAlg_Rejected(t *testing.T) {
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"sub": "mallory", "iss": "nexspence", "uid": "uid-x",
	})
	signed, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)
	s := newSvc()
	_, err = s.ValidateToken(signed)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func flipChar(b byte) byte {
	if b != 'A' {
		return 'A'
	}
	return 'B'
}

func TestValidateToken_TamperedSignature(t *testing.T) {
	s := newSvc()
	token, err := s.GenerateToken("uid-12", "grace", []string{"viewer"})
	require.NoError(t, err)
	// Flip a character in the middle of the signature. Not the last one: an
	// HMAC-SHA256 signature is 32 bytes in 43 base64url characters, so the
	// final character carries only 2 significant bits and the 16 characters
	// sharing them all decode to the same bytes — a quarter of the tokens this
	// test generates would have "tampered" into the identical signature and
	// validated cleanly.
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)
	sig := parts[2]
	require.Greater(t, len(sig), 1)
	mid := len(sig) / 2
	swapped := sig[:mid] + string(flipChar(sig[mid])) + sig[mid+1:]
	require.NotEqual(t, sig, swapped)
	tampered := parts[0] + "." + parts[1] + "." + swapped
	_, err = s.ValidateToken(tampered)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestHashPassword_TooLong_Error(t *testing.T) {
	s := newSvc()
	_, err := s.HashPassword(strings.Repeat("x", 100)) // >72 bytes → bcrypt error
	require.ErrorIs(t, err, bcrypt.ErrPasswordTooLong)
}
