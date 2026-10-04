package sqlstore

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

type jsonDestParseReport domain.DestParseReport

func (v jsonDestParseReport) Value() (driver.Value, error) {
	b, err := json.Marshal(domain.DestParseReport(v))
	return string(b), err
}
func (jsonDestParseReport) GormDataType() string                          { return "text" }
func (jsonDestParseReport) GormDBDataType(*gorm.DB, *schema.Field) string { return "text" }
func (v *jsonDestParseReport) Scan(value any) error {
	*v = jsonDestParseReport{}
	switch raw := value.(type) {
	case nil:
		return nil
	case string:
		return json.Unmarshal([]byte(raw), (*domain.DestParseReport)(v))
	case []byte:
		return json.Unmarshal(raw, (*domain.DestParseReport)(v))
	default:
		return fmt.Errorf("unsupported destination report scan: %T", value)
	}
}

func destReportFromDomain(v *domain.DestParseReport) *jsonDestParseReport {
	if v == nil {
		return nil
	}
	result := jsonDestParseReport(*v)
	result.Samples = append([]domain.DestParseSample{}, v.Samples...)
	return &result
}
func destReportToDomain(v *jsonDestParseReport) *domain.DestParseReport {
	if v == nil {
		return nil
	}
	result := domain.DestParseReport(*v)
	result.Samples = append([]domain.DestParseSample{}, v.Samples...)
	return &result
}
func equalDestReport(a, b *jsonDestParseReport) bool { return reflect.DeepEqual(a, b) }
