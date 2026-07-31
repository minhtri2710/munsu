package cli

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Encode serializes a contract response at the output boundary.
func Encode(value any, output string) (string, error) {
	switch output {
	case OutputJSON:
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return "", err
		}
		return string(data), nil
	case OutputTOON:
		var builder strings.Builder
		if err := encodeValue(&builder, reflect.ValueOf(value), 0, ""); err != nil {
			return "", err
		}
		return builder.String(), nil
	default:
		return "", fmt.Errorf("unsupported output %q (supported: toon, json)", output)
	}
}

func encodeValue(builder *strings.Builder, value reflect.Value, depth int, key string) error {
	for value.Kind() == reflect.Interface {
		if value.IsNil() {
			writeField(builder, depth, key, "null")
			return nil
		}
		value = value.Elem()
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			writeField(builder, depth, key, "null")
			return nil
		}
		elem := value.Elem()
		if elem.Kind() == reflect.Struct && key != "" {
			writeField(builder, depth, key, "")
			return encodeValue(builder, elem, depth+1, "")
		}
		return encodeValue(builder, elem, depth, key)
	}

	switch value.Kind() {
	case reflect.Struct:
		typeOf := value.Type()
		for index := range value.NumField() {
			field := typeOf.Field(index)
			if field.PkgPath != "" {
				continue
			}
			name, omitEmpty := jsonFieldName(field)
			if name == "" {
				continue
			}
			fieldValue := value.Field(index)
			if omitEmpty && fieldValue.IsZero() {
				continue
			}
			if fieldValue.Kind() == reflect.Struct {
				writeField(builder, depth, name, "")
				if err := encodeValue(builder, fieldValue, depth+1, ""); err != nil {
					return err
				}
				continue
			}
			if err := encodeValue(builder, fieldValue, depth, name); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		if value.Len() == 0 {
			writeField(builder, depth, key, "[]")
			return nil
		}
		if isScalar(value.Index(0)) {
			values := make([]string, value.Len())
			for index := range value.Len() {
				values[index] = scalar(value.Index(index))
			}
			writeField(builder, depth, fmt.Sprintf("%s[%d]", key, value.Len()), strings.Join(values, ","))
			return nil
		}
		if value.Index(0).Kind() == reflect.Struct {
			return encodeTable(builder, value, depth, key)
		}
		writeField(builder, depth, key, "[]")
	default:
		writeField(builder, depth, key, scalar(value))
	}
	return nil
}

func encodeTable(builder *strings.Builder, values reflect.Value, depth int, key string) error {
	typeOf := values.Index(0).Type()
	var fields []int
	var names []string
	for index := range typeOf.NumField() {
		field := typeOf.Field(index)
		if field.PkgPath != "" {
			continue
		}
		name, _ := jsonFieldName(field)
		if name == "" {
			continue
		}
		fields = append(fields, index)
		names = append(names, name)
	}
	writeField(builder, depth, fmt.Sprintf("%s[%d]{%s}", key, values.Len(), strings.Join(names, ",")), "")
	for row := range values.Len() {
		rowValues := make([]string, len(fields))
		for column, field := range fields {
			rowValues[column] = scalar(values.Index(row).Field(field))
		}
		writeField(builder, depth+1, "", strings.Join(rowValues, ","))
	}
	return nil
}

func jsonFieldName(field reflect.StructField) (string, bool) {
	tag := field.Tag.Get("json")
	if tag == "-" {
		return "", false
	}
	parts := strings.Split(tag, ",")
	name := parts[0]
	if name == "" {
		name = field.Name
	}
	return name, len(parts) > 1 && parts[1] == "omitempty"
}

func isScalar(value reflect.Value) bool {
	for value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return true
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.Bool, reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

func scalar(value reflect.Value) string {
	for value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return "null"
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.Bool:
		return strconv.FormatBool(value.Bool())
	case reflect.String:
		return toonString(value.String())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(value.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(value.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(value.Float(), 'g', -1, 64)
	default:
		return "null"
	}
}

func toonString(value string) string {
	if value == SchemaVersion || value == "" || value == "true" || value == "false" || value == "null" || strings.HasPrefix(value, "-") || strings.ContainsAny(value, ":[]{}\",\\\n\r\t") || strings.TrimSpace(value) != value || numericLike(value) {
		return strconv.Quote(value)
	}
	return value
}

func numericLike(value string) bool {
	if value == "" {
		return false
	}
	_, err := strconv.ParseFloat(value, 64)
	return err == nil
}

func writeField(builder *strings.Builder, depth int, key, value string) {
	if builder.Len() > 0 {
		builder.WriteByte('\n')
	}
	builder.WriteString(strings.Repeat("  ", depth))
	if key == "" {
		builder.WriteString(value)
		return
	}
	builder.WriteString(key)
	builder.WriteString(":")
	if value != "" {
		builder.WriteByte(' ')
		builder.WriteString(value)
	}
}
