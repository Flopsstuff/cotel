package ingest

import (
	"fmt"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
)

func attrsToMap(kvs []*commonpb.KeyValue) map[string]any {
	m := make(map[string]any, len(kvs))
	for _, kv := range kvs {
		m[kv.Key] = anyVal(kv.Value)
	}
	return m
}

func anyVal(v *commonpb.AnyValue) any {
	if v == nil {
		return nil
	}
	switch t := v.Value.(type) {
	case *commonpb.AnyValue_StringValue:
		return t.StringValue
	case *commonpb.AnyValue_IntValue:
		return t.IntValue
	case *commonpb.AnyValue_DoubleValue:
		return t.DoubleValue
	case *commonpb.AnyValue_BoolValue:
		return t.BoolValue
	default:
		return fmt.Sprintf("%v", v)
	}
}

func attrStr(kvs []*commonpb.KeyValue, key string) string {
	for _, kv := range kvs {
		if kv.Key == key {
			if s, ok := kv.Value.Value.(*commonpb.AnyValue_StringValue); ok {
				return s.StringValue
			}
		}
	}
	return ""
}

func toInt64(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case float64:
		return int64(t)
	case string:
		var n int64
		fmt.Sscanf(t, "%d", &n)
		return n
	}
	return 0
}

func toFloat64(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int64:
		return float64(t)
	case string:
		var f float64
		fmt.Sscanf(t, "%f", &f)
		return f
	}
	return 0
}
