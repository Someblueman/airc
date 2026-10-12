package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
)

// AddTool adds a tool whose arguments decode into In and whose structured
// output is Out. Both schemas are derived from the types' json and jsonschema
// struct tags, as the MCP SDK derived them. Arguments are validated against
// the input schema before the handler runs. A handler error becomes an error
// result carrying the error text; a nil result carries Out as JSON text.
func AddTool[In, Out any](s *Server, t *Tool, handler func(context.Context, *Call, In) (*Result, Out, error)) {
	tool := *t
	input := SchemaFor(reflect.TypeFor[In]())
	tool.InputSchema = input
	tool.OutputSchema = SchemaFor(reflect.TypeFor[Out]())
	tool.Handler = func(ctx context.Context, call *Call) *Result {
		arguments := call.Arguments
		if arguments == nil || string(arguments) == "null" {
			arguments = json.RawMessage("{}")
		}
		var value any
		if err := json.Unmarshal(arguments, &value); err != nil {
			return errorResult(err)
		}
		if err := validate(input, value, "root"); err != nil {
			return errorResult(fmt.Errorf("validating \"arguments\": %w", err))
		}
		var in In
		if err := json.Unmarshal(arguments, &in); err != nil {
			return errorResult(err)
		}
		result, out, err := handler(ctx, call, in)
		if err != nil {
			return errorResult(err)
		}
		if result == nil {
			result = &Result{}
		}
		encoded, err := json.Marshal(out)
		if err != nil {
			return errorResult(fmt.Errorf("marshaling output: %w", err))
		}
		result.StructuredContent = json.RawMessage(encoded)
		if result.Content == nil {
			result.Content = Text(string(encoded))
		}
		return result
	}
	s.AddTool(&tool)
}

func errorResult(err error) *Result {
	return &Result{Content: Text(err.Error()), IsError: true}
}

// SchemaFor derives a JSON Schema from a Go type: a struct is a closed object
// whose fields without omitempty are required, a slice may also be null, a
// pointer adds null to its element's type, and an interface accepts anything.
// A jsonschema struct tag is the property's description.
func SchemaFor(t reflect.Type) any {
	switch t.Kind() {
	case reflect.Interface:
		return true
	case reflect.Pointer:
		schema := SchemaFor(t.Elem())
		if object, ok := schema.(map[string]any); ok && t.Elem().Kind() != reflect.Struct {
			if kind, ok := object["type"].(string); ok {
				object["type"] = []string{"null", kind}
			}
		}
		return schema
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": []string{"null", "array"}, "items": SchemaFor(t.Elem())}
	case reflect.Struct:
		schema := map[string]any{"type": "object", "additionalProperties": false}
		properties := map[string]any{}
		var required []string
		for field := range t.Fields() {
			if !field.IsExported() {
				continue
			}
			name, options, _ := strings.Cut(field.Tag.Get("json"), ",")
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			property := SchemaFor(field.Type)
			if description := field.Tag.Get("jsonschema"); description != "" {
				if object, ok := property.(map[string]any); ok {
					object["description"] = description
				}
			}
			properties[name] = property
			if !slices.Contains(strings.Split(options, ","), "omitempty") {
				required = append(required, name)
			}
		}
		if len(properties) > 0 {
			schema["properties"] = properties
		}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	}
	panic(fmt.Sprintf("mcpserver: no schema for %s", t))
}

// validate checks the subset of JSON Schema that SchemaFor produces.
func validate(schema, value any, path string) error {
	object, ok := schema.(map[string]any)
	if !ok {
		return nil // true: anything
	}
	if kinds, ok := object["type"]; ok {
		var allowed []string
		switch kinds := kinds.(type) {
		case string:
			allowed = []string{kinds}
		case []string:
			allowed = kinds
		}
		if !slices.ContainsFunc(allowed, func(kind string) bool { return hasType(value, kind) }) {
			want := fmt.Sprintf("%q", allowed[0])
			if len(allowed) > 1 {
				want = fmt.Sprintf("one of %q", strings.Join(allowed, ", "))
			}
			return fmt.Errorf("validating %s: type: %v has type %q, want %s", path, value, typeName(value), want)
		}
	}
	switch value := value.(type) {
	case map[string]any:
		properties, _ := object["properties"].(map[string]any)
		if required, ok := object["required"].([]string); ok {
			var missing []string
			for _, name := range required {
				if _, ok := value[name]; !ok {
					missing = append(missing, name)
				}
			}
			if len(missing) > 0 {
				return fmt.Errorf("validating %s: required: missing properties: [%s]", path, strings.Join(quoted(missing), ","))
			}
		}
		var extra []string
		for _, name := range slices.Sorted(func(yield func(string) bool) {
			for name := range value {
				if !yield(name) {
					return
				}
			}
		}) {
			property, ok := properties[name]
			if !ok {
				extra = append(extra, name)
				continue
			}
			if err := validate(property, value[name], "/properties/"+name); err != nil {
				return fmt.Errorf("validating %s: %w", path, err)
			}
		}
		if len(extra) > 0 && object["additionalProperties"] == false {
			return fmt.Errorf("validating %s: unexpected additional properties [%s]", path, strings.Join(quoted(extra), ","))
		}
	case []any:
		if items, ok := object["items"]; ok {
			for _, item := range value {
				if err := validate(items, item, path+"/items"); err != nil {
					return fmt.Errorf("validating %s: %w", path, err)
				}
			}
		}
	}
	return nil
}

func hasType(value any, kind string) bool {
	switch value := value.(type) {
	case nil:
		return kind == "null"
	case bool:
		return kind == "boolean"
	case string:
		return kind == "string"
	case float64:
		return kind == "number" || kind == "integer" && value == math.Trunc(value) && !math.IsInf(value, 0)
	case []any:
		return kind == "array"
	case map[string]any:
		return kind == "object"
	}
	return false
}

func typeName(value any) string {
	switch value := value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64:
		if value == math.Trunc(value) {
			return "integer"
		}
		return "number"
	case []any:
		return "array"
	}
	return "object"
}

func quoted(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = fmt.Sprintf("%q", v)
	}
	return out
}
