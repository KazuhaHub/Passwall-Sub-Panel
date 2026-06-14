package sqlstore

import (
	"reflect"
	"strings"
	"testing"
)

// TestSchemaNoDefaultOnTextColumns guards against the cross-dialect trap that
// broke the v3.7.0-beta.5 totp_secret column on MySQL: a TEXT/BLOB/JSON column
// with a DEFAULT clause. MySQL rejects "BLOB, TEXT, GEOMETRY or JSON column ...
// can't have a default value" (Error 1101) at ALTER/CREATE time. Our tests run
// on SQLite (which tolerates it), so without this static check the defect only
// surfaces on a real MySQL deployment. A column that needs a non-null backfill
// must be varchar (size:N) — varchar carries a DEFAULT on all three dialects.
func TestSchemaNoDefaultOnTextColumns(t *testing.T) {
	textishTypes := []string{
		"text", "tinytext", "mediumtext", "longtext",
		"blob", "tinyblob", "mediumblob", "longblob",
		"json", "jsonb", "geometry",
	}
	isTextish := func(colType string) bool {
		colType = strings.ToLower(strings.TrimSpace(colType))
		for _, tt := range textishTypes {
			if colType == tt || strings.HasPrefix(colType, tt+"(") {
				return true
			}
		}
		return false
	}

	var walk func(rt reflect.Type, model string)
	walk = func(rt reflect.Type, model string) {
		for rt.Kind() == reflect.Ptr {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct {
			return
		}
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if f.Anonymous { // embedded struct — recurse
				walk(f.Type, model)
				continue
			}
			tag := f.Tag.Get("gorm")
			if tag == "" {
				continue
			}
			var colType string
			var hasDefault bool
			for _, part := range strings.Split(tag, ";") {
				p := strings.TrimSpace(part)
				lp := strings.ToLower(p)
				switch {
				case strings.HasPrefix(lp, "type:"):
					colType = p[len("type:"):]
				case lp == "default" || strings.HasPrefix(lp, "default:"):
					hasDefault = true
				}
			}
			if hasDefault && isTextish(colType) {
				t.Errorf("%s.%s declares type %q WITH a default — MySQL forbids a DEFAULT on TEXT/BLOB/JSON (Error 1101); use varchar (size:N) instead",
					model, f.Name, colType)
			}
		}
	}

	for _, m := range schemaModels {
		rt := reflect.TypeOf(m)
		walk(rt, rt.String())
	}
}
