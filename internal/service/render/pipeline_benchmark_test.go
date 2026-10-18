package render

import (
	"encoding/json"
	"fmt"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Measure the complete binding/pruning/naming pass, not just name allocation.
func BenchmarkSubscriptionLinePipeline(b *testing.B) {
	for _, size := range []int{128, 512} {
		for _, client := range []string{"mihomo", "sing-box"} {
			b.Run(fmt.Sprintf("%s/%d", client, size), func(b *testing.B) {
				items := make([]renderItem, size)
				for i := range items {
					items[i] = renderItem{node: &domain.Node{ID: int64(i + 1)}, name: fmt.Sprintf("line-%d", i)}
				}
				catalog, items := newLineNameCatalog(items, false)
				definitions := make([]map[string]any, len(items))
				members := []string{}
				key := "name"
				if client == "sing-box" {
					key = "tag"
				}
				for i, item := range items {
					definitions[i] = map[string]any{key: item.name, "type": "vless", "server": "fixture.invalid", "uuid": "12345678-1234-4abc-8def-123456789abc"}
					if i%8 == 0 {
						members = append(members, item.name)
					}
				}
				generated := emittedProxyNames(definitions, key)
				origin := markGeneratedLines(definitions, key, generated)
				catalog.origin = origin
				var raw []byte
				if client == "mihomo" {
					raw, _ = yaml.Marshal(map[string]any{"proxies": definitions, "proxy-groups": []any{map[string]any{"name": "G", "type": "select", "proxies": members}}, "rules": []string{"MATCH,G"}})
				} else {
					outbounds := append(definitions, map[string]any{"tag": "G", "type": "selector", "outbounds": members})
					raw, _ = json.Marshal(map[string]any{"outbounds": outbounds, "route": map[string]any{"final": "G"}})
				}
				body := string(raw)
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					var err error
					if client == "mihomo" {
						_, err = catalog.finishMihomo(body, generated, origin)
					} else {
						_, err = catalog.finishSingBox(body, generated, origin)
					}
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
