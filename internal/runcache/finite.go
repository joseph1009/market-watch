package runcache

import (
	"encoding"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
)

// finite is v as maps, slices and plain values that encoding/json takes, with
// every NaN or infinity as null. The market's figures use NaN for a figure
// the history is too short for, which JSON has no word for: a leader listed
// for eighteen months has no two-year return, and without this its whole
// list went unsaved. A struct comes out as a map, so its fields are written
// in alphabetical order rather than as declared.
func finite(v reflect.Value) any {
	if !v.IsValid() {
		return nil
	}
	if v.CanInterface() {
		switch v.Interface().(type) {
		case json.Marshaler, encoding.TextMarshaler:
			return v.Interface() // time.Time and the like write themselves
		}
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return finite(v.Elem())
	case reflect.Float32, reflect.Float64:
		if f := v.Float(); math.IsNaN(f) || math.IsInf(f, 0) {
			return nil
		}
		return v.Interface()
	case reflect.Struct:
		out := map[string]any{}
		fields(out, v)
		return out
	case reflect.Map:
		if v.IsNil() {
			return nil
		}
		out := make(map[string]any, v.Len())
		for it := v.MapRange(); it.Next(); {
			out[fmt.Sprint(it.Key().Interface())] = finite(it.Value())
		}
		return out
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return nil
		}
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return v.Interface()
		}
		out := make([]any, v.Len())
		for i := range out {
			out[i] = finite(v.Index(i))
		}
		return out
	}
	return v.Interface()
}

// fields adds a struct's fields to out as encoding/json names them: by tag,
// an embedded struct's fields alongside its own, empty ones left out where
// the tag says omitempty, and none the tag hides.
func fields(out map[string]any, v reflect.Value) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		fv := v.Field(i)
		if f.Anonymous && name == "" {
			if fv.Kind() == reflect.Pointer {
				if fv.IsNil() {
					continue
				}
				fv = fv.Elem()
			}
			if fv.Kind() == reflect.Struct {
				fields(out, fv)
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		if (strings.Contains(opts, "omitempty") || strings.Contains(opts, "omitzero")) && fv.IsZero() {
			continue
		}
		out[name] = finite(fv)
	}
}
