package auth

import (
	"errors"
	"testing"

	"github.com/crewjam/saml"
)

// GO-2026-6909: crewjam dereferences an assertion's Subject without a nil check
// after verification. samlguard refuses that shape in a plaintext assertion, but cannot see inside an
// EncryptedAssertion, so the panic is still reachable on the decrypted path.
// It must surface as an ordinary refused login, not escape to gin.Recovery as a
// 500 with no auth event.
func TestVerifyResponse_TurnsAPanicIntoAnError(t *testing.T) {
	a, err := verifyResponse(func() (*saml.Assertion, error) {
		var subject *saml.Subject
		_ = subject.SubjectConfirmations // the nil dereference crewjam performs
		return &saml.Assertion{}, nil
	})
	if a != nil {
		t.Fatalf("a panicking parse returned an assertion: %+v", a)
	}
	if !errors.Is(err, errSAMLParsePanic) {
		t.Fatalf("err = %v, want errSAMLParsePanic", err)
	}
}

func TestVerifyResponse_PassesThroughResultAndError(t *testing.T) {
	want := &saml.Assertion{ID: "id-1"}
	if got, err := verifyResponse(func() (*saml.Assertion, error) { return want, nil }); got != want || err != nil {
		t.Fatalf("got (%v, %v), want (%v, nil)", got, err, want)
	}
	boom := errors.New("boom")
	if got, err := verifyResponse(func() (*saml.Assertion, error) { return nil, boom }); got != nil || !errors.Is(err, boom) {
		t.Fatalf("got (%v, %v), want (nil, boom)", got, err)
	}
}
