package handler

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/subdevice"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// deviceMaterial stands in for the panel secret the composition root hands
// subdevice.NewHasher.
const deviceMaterial = "sub-device-test-secret-material"

// deviceRequest is a gin context for a subscription fetch carrying the given
// request headers, plus the recorder its response would go to.
func deviceRequest(headers map[string]string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/sub/some-token", nil)
	for k, v := range headers {
		c.Request.Header.Set(k, v)
	}
	return c, rec
}

// A client that declares everything: an id and all three label headers.
func fullDeclaration(hwid string) map[string]string {
	return map[string]string{
		subdevice.HeaderHWID:        hwid,
		subdevice.HeaderDeviceOS:    "iOS",
		subdevice.HeaderOSVersion:   "17.5",
		subdevice.HeaderDeviceModel: "iPhone15,2",
	}
}

func deviceHandler(h *subdevice.Hasher) *SubHandler {
	// Built through the constructor so a constructor that forgets to store
	// the hasher reads as "capture never happens", not as a passing test.
	return NewSubHandler(nil, nil, nil, nil, nil, nil, nil, h)
}

func TestDeclaredDevice_ValidHeadersGiveTheKeyedIDAndLabel(t *testing.T) {
	hasher := subdevice.NewHasher(deviceMaterial)
	c, rec := deviceRequest(fullDeclaration("abcd1234-5678-90ef"))

	id, label := deviceHandler(hasher).declaredDevice(c, 7, ports.UISettings{})

	if want := hasher.ID(7, "abcd1234-5678-90ef"); id != want {
		t.Errorf("id = %q, want the keyed per-user digest %q", id, want)
	}
	if want := "iOS 17.5 · iPhone15,2"; label != want {
		t.Errorf("label = %q, want %q", label, want)
	}
	// Reading the declaration must not answer it: no header, no status, no
	// body — a client must not be able to tell whether it was recorded.
	if n := len(rec.Header()); n != 0 {
		t.Errorf("declaredDevice wrote %d response header(s): %v", n, rec.Header())
	}
	if c.Writer.Written() {
		t.Error("declaredDevice wrote a response")
	}
}

// A client that changes the case of its id between releases is still one
// device; the id is keyed on the normalized value, not the raw header.
func TestDeclaredDevice_CaseDoesNotSplitADevice(t *testing.T) {
	h := deviceHandler(subdevice.NewHasher(deviceMaterial))
	upper, _ := deviceRequest(fullDeclaration("ABCD1234-5678-90EF"))
	lower, _ := deviceRequest(fullDeclaration("abcd1234-5678-90ef"))

	idUpper, _ := h.declaredDevice(upper, 7, ports.UISettings{})
	idLower, _ := h.declaredDevice(lower, 7, ports.UISettings{})
	if idUpper == "" || idUpper != idLower {
		t.Fatalf("upper-case id %q vs lower-case id %q: want the same non-empty id", idUpper, idLower)
	}
}

// Without a usable id the fetch is anonymous: no id, and no label either — a
// label alone counts nothing and would still be a device description kept
// for a client that declared no identity.
func TestDeclaredDevice_InvalidHWIDRecordsNothing(t *testing.T) {
	h := deviceHandler(subdevice.NewHasher(deviceMaterial))
	for name, hwid := range map[string]string{
		"absent":      "",
		"too short":   "abc1234",
		"inner space": "abcd 1234 efgh",
		"non-ascii":   "abcd1234é",
	} {
		t.Run(name, func(t *testing.T) {
			headers := fullDeclaration(hwid)
			if hwid == "" {
				delete(headers, subdevice.HeaderHWID)
			}
			c, _ := deviceRequest(headers)
			if id, label := h.declaredDevice(c, 7, ports.UISettings{}); id != "" || label != "" {
				t.Errorf("declaredDevice = (%q, %q), want (\"\", \"\")", id, label)
			}
		})
	}
}

func TestDeclaredDevice_CaptureOffRecordsNothing(t *testing.T) {
	h := deviceHandler(subdevice.NewHasher(deviceMaterial))
	c, _ := deviceRequest(fullDeclaration("abcd1234-5678-90ef"))
	if id, label := h.declaredDevice(c, 7, ports.UISettings{RiskHWIDCaptureOff: true}); id != "" || label != "" {
		t.Errorf("capture off: declaredDevice = (%q, %q), want (\"\", \"\")", id, label)
	}
}

// A nil hasher is the "no secret to key it with" wiring; it must record
// nothing rather than, say, a label without an id.
func TestDeclaredDevice_NilHasherRecordsNothing(t *testing.T) {
	c, _ := deviceRequest(fullDeclaration("abcd1234-5678-90ef"))
	if id, label := deviceHandler(nil).declaredDevice(c, 7, ports.UISettings{}); id != "" || label != "" {
		t.Errorf("nil hasher: declaredDevice = (%q, %q), want (\"\", \"\")", id, label)
	}
}

// THE RAW HEADER MUST HAVE NO WAY OUT OF declaredDevice BUT THE DIGEST.
//
// The function holds the raw x-hwid in a local, on the public endpoint, for
// every fetch. A log line there (even a Debug "invalid hwid" with the value)
// would put the identifier the digest exists to hide into the server log, and
// an error result would invite the caller to log it or to fail the fetch —
// which would let a client probe whether capture is on. So this reads the
// source: no selector on the log package, no error among the results, and the
// gin context used for nothing but reading request headers (no status, no
// response header, no abort).
func TestDeclaredDeviceHasNoLogOrErrorPath(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "sub.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var fn *ast.FuncDecl
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv != nil && fd.Name.Name == "declaredDevice" {
			fn = fd
		}
	}
	if fn == nil {
		t.Fatal("declaredDevice not found in sub.go — the guard is pointed at nothing")
	}
	if fn.Type.Results != nil {
		for _, r := range fn.Type.Results.List {
			if id, ok := r.Type.(*ast.Ident); ok && id.Name == "error" {
				t.Error("declaredDevice returns an error: a capture failure must be an anonymous fetch, not something a caller logs or answers")
			}
		}
	}
	ctxName := ""
	if ps := fn.Type.Params.List; len(ps) > 0 && len(ps[0].Names) > 0 {
		ctxName = ps[0].Names[0].Name
	}
	if ctxName == "" {
		t.Fatal("declaredDevice has no named first parameter — expected the gin context")
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		x, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		switch {
		case x.Name == "log":
			t.Errorf("%s: declaredDevice calls log.%s — it holds the raw x-hwid and must never log",
				fset.Position(sel.Pos()), sel.Sel.Name)
		case x.Name == ctxName && sel.Sel.Name != "GetHeader":
			t.Errorf("%s: declaredDevice uses %s.%s — it may only read request headers, never touch the response",
				fset.Position(sel.Pos()), ctxName, sel.Sel.Name)
		}
		return true
	})
	// And the body must actually read through the context, or the check above
	// passes vacuously on a function that takes the headers some other way.
	if !strings.Contains(sourceOf(t, fset, fn), ctxName+".GetHeader(") {
		t.Errorf("declaredDevice never calls %s.GetHeader — the guard no longer describes how it reads the declaration", ctxName)
	}
}

func sourceOf(t *testing.T, fset *token.FileSet, fn *ast.FuncDecl) string {
	t.Helper()
	src := readHandlerSource(t, "sub.go")
	start, end := fset.Position(fn.Pos()).Offset, fset.Position(fn.End()).Offset
	return src[start:end]
}
