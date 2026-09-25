// Package bind copies a configuration tree into Go values.
//
// A tree is what Consul KV (or a YAML/JSON document) decodes to: nested
// map[string]any whose leaves are strings or JSON/YAML scalars, and []any
// for lists. Binding is explicit and bounded: only exported fields are set,
// no unsafe code is used, and every problem is reported with the full key
// path instead of stopping at the first one.
package bind

import (
	"encoding"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Error describes a value that could not be bound.
type Error struct {
	// Key is the slash separated path, e.g. "database/max-conns".
	Key string
	// Type is the Go type of the destination.
	Type string
	Err  error
}

func (e *Error) Error() string {
	return fmt.Sprintf("consulx: config key %q (%s): %v", e.Key, e.Type, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// ErrRequired is wrapped by errors for missing required keys.
var ErrRequired = errors.New("required key is missing")

// Options tune binding.
type Options struct {
	// Tag is the struct tag holding key names. Default "consul".
	Tag string
	// ErrorUnused reports keys that match no field.
	ErrorUnused bool
}

// Bind copies tree into dst, which must be a non-nil pointer to a struct.
// Missing keys keep the destination's current value (or its `default`).
func Bind(tree map[string]any, dst any, opts Options) error {
	if opts.Tag == "" {
		opts.Tag = "consul"
	}
	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("consulx: bind destination must be a non-nil pointer to a struct, got %T", dst)
	}
	b := &binder{opts: opts}
	b.bindStruct("", tree, rv.Elem())
	return errors.Join(b.errs...)
}

type binder struct {
	opts Options
	errs []error
}

func (b *binder) fail(key string, t reflect.Type, err error) {
	b.errs = append(b.errs, &Error{Key: key, Type: t.String(), Err: err})
}

type fieldSpec struct {
	name     string
	required bool
	skip     bool
}

func (b *binder) spec(f reflect.StructField) fieldSpec {
	tag, ok := f.Tag.Lookup(b.opts.Tag)
	if !ok {
		return fieldSpec{name: f.Name}
	}
	name, rest, _ := strings.Cut(tag, ",")
	if name == "-" {
		return fieldSpec{skip: true}
	}
	s := fieldSpec{name: name}
	if s.name == "" {
		s.name = f.Name
	}
	for opt := range strings.SplitSeq(rest, ",") {
		if strings.TrimSpace(opt) == "required" {
			s.required = true
		}
	}
	return s
}

// normalize makes "MaxConns", "max-conns", "max_conns" and "maxconns" equal.
func normalize(s string) string {
	return strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "").Replace(s))
}

// lookup finds a key: exact match first, then normalised match.
func lookup(tree map[string]any, name string) (string, any, bool) {
	if v, ok := tree[name]; ok {
		return name, v, true
	}
	n := normalize(name)
	keys := make([]string, 0, len(tree))
	for k := range tree {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic when several keys normalise alike
	for _, k := range keys {
		if normalize(k) == n {
			return k, tree[k], true
		}
	}
	return "", nil, false
}

func join(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "/" + key
}

func (b *binder) bindStruct(path string, tree map[string]any, v reflect.Value) {
	t := v.Type()
	used := map[string]bool{}
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		fv := v.Field(i)
		spec := b.spec(f)
		if spec.skip {
			continue
		}
		// Embedded structs without a tag are flattened, like encoding/json.
		if f.Anonymous && !hasTag(f, b.opts.Tag) {
			if et := indirectType(f.Type); et.Kind() == reflect.Struct {
				b.bindStruct(path, tree, allocate(fv))
				continue
			}
		}
		key, raw, found := lookup(tree, spec.name)
		if !found {
			switch def, hasDef := f.Tag.Lookup("default"); {
			case hasDef:
				b.assign(join(path, spec.name), def, fv)
			case spec.required:
				b.fail(join(path, spec.name), f.Type, ErrRequired)
			case f.Type.Kind() == reflect.Struct && !f.Type.Implements(textUnmarshalerType) &&
				!reflect.PointerTo(f.Type).Implements(textUnmarshalerType):
				// A missing section still gets its defaults and its
				// required keys checked. Pointer sections stay nil: they
				// are optional by design.
				b.bindStruct(join(path, spec.name), map[string]any{}, fv)
			}
			continue
		}
		used[key] = true
		b.assign(join(path, key), raw, fv)
	}
	if b.opts.ErrorUnused {
		for k := range tree {
			if !used[k] && !matchesAnyField(t, k, b.opts.Tag) {
				b.errs = append(b.errs, &Error{Key: join(path, k), Type: t.String(), Err: errors.New("unknown key")})
			}
		}
	}
}

func hasTag(f reflect.StructField, tag string) bool {
	_, ok := f.Tag.Lookup(tag)
	return ok
}

func matchesAnyField(t reflect.Type, key, tag string) bool {
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Anonymous && indirectType(f.Type).Kind() == reflect.Struct && matchesAnyField(indirectType(f.Type), key, tag) {
			return true
		}
		name := f.Name
		if tv, ok := f.Tag.Lookup(tag); ok {
			if n, _, _ := strings.Cut(tv, ","); n != "" {
				name = n
			}
		}
		if name == key || normalize(name) == normalize(key) {
			return true
		}
	}
	return false
}

func indirectType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// allocate follows pointers, allocating nil ones, and returns the target.
func allocate(v reflect.Value) reflect.Value {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		v = v.Elem()
	}
	return v
}

var (
	durationType        = reflect.TypeFor[time.Duration]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// assign stores raw into v, converting as needed.
func (b *binder) assign(key string, raw any, v reflect.Value) {
	// TextUnmarshaler wins over the kind-based conversion (net.IP, custom types).
	if v.CanAddr() && v.Addr().Type().Implements(textUnmarshalerType) && v.Kind() != reflect.Pointer {
		s, ok := scalar(raw)
		if !ok {
			b.fail(key, v.Type(), errors.New("expected a scalar value"))
			return
		}
		if err := v.Addr().Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(s)); err != nil {
			b.fail(key, v.Type(), err)
		}
		return
	}

	switch v.Kind() {
	case reflect.Pointer:
		if raw == nil {
			v.SetZero()
			return
		}
		tmp := reflect.New(v.Type().Elem())
		before := len(b.errs)
		b.assign(key, raw, tmp.Elem())
		if len(b.errs) == before {
			v.Set(tmp)
		}
		return
	case reflect.Struct:
		m, ok := raw.(map[string]any)
		if !ok {
			b.fail(key, v.Type(), errors.New("expected an object"))
			return
		}
		b.bindStruct(key, m, v)
		return
	case reflect.Map:
		b.assignMap(key, raw, v)
		return
	case reflect.Slice:
		b.assignSlice(key, raw, v)
		return
	case reflect.Interface:
		if v.NumMethod() == 0 {
			v.Set(reflect.ValueOf(raw))
			return
		}
	}

	s, ok := scalar(raw)
	if !ok {
		b.fail(key, v.Type(), errors.New("expected a scalar value"))
		return
	}
	if err := setScalar(v, s); err != nil {
		b.fail(key, v.Type(), err)
	}
}

func (b *binder) assignMap(key string, raw any, v reflect.Value) {
	m, ok := raw.(map[string]any)
	if !ok {
		b.fail(key, v.Type(), errors.New("expected an object"))
		return
	}
	if v.Type().Key().Kind() != reflect.String {
		b.fail(key, v.Type(), errors.New("only maps with string keys are supported"))
		return
	}
	out := reflect.MakeMapWithSize(v.Type(), len(m))
	for k, item := range m {
		ev := reflect.New(v.Type().Elem()).Elem()
		before := len(b.errs)
		b.assign(join(key, k), item, ev)
		if len(b.errs) == before {
			out.SetMapIndex(reflect.ValueOf(k).Convert(v.Type().Key()), ev)
		}
	}
	v.Set(out)
}

// assignSlice accepts a list, a KV folder with numeric keys ("0", "1", ...)
// or a comma separated string.
func (b *binder) assignSlice(key string, raw any, v reflect.Value) {
	var items []any
	switch r := raw.(type) {
	case []any:
		items = r
	case map[string]any:
		idx := make([]int, 0, len(r))
		for k := range r {
			n, err := strconv.Atoi(k)
			if err != nil || n < 0 {
				b.fail(key, v.Type(), fmt.Errorf("list folder has non-numeric key %q", k))
				return
			}
			idx = append(idx, n)
		}
		slices.Sort(idx)
		for _, n := range idx {
			items = append(items, r[strconv.Itoa(n)])
		}
	default:
		s, ok := scalar(raw)
		if !ok {
			b.fail(key, v.Type(), errors.New("expected a list"))
			return
		}
		if v.Type().Elem().Kind() == reflect.Uint8 { // []byte
			v.SetBytes([]byte(s))
			return
		}
		if strings.TrimSpace(s) != "" {
			for part := range strings.SplitSeq(s, ",") {
				items = append(items, strings.TrimSpace(part))
			}
		}
	}
	out := reflect.MakeSlice(v.Type(), len(items), len(items))
	for i, item := range items {
		b.assign(key+"/"+strconv.Itoa(i), item, out.Index(i))
	}
	v.Set(out)
}

// scalar renders a leaf as text. JSON/YAML numbers and booleans are
// rendered the same way a KV string would be written.
func scalar(raw any) (string, bool) {
	switch r := raw.(type) {
	case string:
		return r, true
	case []byte:
		return string(r), true
	case bool:
		return strconv.FormatBool(r), true
	case int:
		return strconv.Itoa(r), true
	case int64:
		return strconv.FormatInt(r, 10), true
	case uint64:
		return strconv.FormatUint(r, 10), true
	case float64:
		if r == math.Trunc(r) && math.Abs(r) < 1e15 {
			return strconv.FormatInt(int64(r), 10), true
		}
		return strconv.FormatFloat(r, 'g', -1, 64), true
	case nil:
		return "", true
	default:
		return "", false
	}
}

func setScalar(v reflect.Value, s string) error {
	s = strings.TrimSpace(s)
	if v.Type() == durationType {
		d, err := time.ParseDuration(s)
		if err != nil {
			return errors.New(`expected a duration such as "10s"`)
		}
		v.SetInt(int64(d))
		return nil
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString(s)
	case reflect.Bool:
		x, err := strconv.ParseBool(s)
		if err != nil {
			return errors.New("expected a boolean")
		}
		v.SetBool(x)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		x, err := strconv.ParseInt(s, 10, v.Type().Bits())
		if err != nil {
			return fmt.Errorf("expected an integer that fits %s", v.Type())
		}
		v.SetInt(x)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		x, err := strconv.ParseUint(s, 10, v.Type().Bits())
		if err != nil {
			return fmt.Errorf("expected a non-negative integer that fits %s", v.Type())
		}
		v.SetUint(x)
	case reflect.Float32, reflect.Float64:
		x, err := strconv.ParseFloat(s, v.Type().Bits())
		if err != nil {
			return errors.New("expected a number")
		}
		v.SetFloat(x)
	default:
		return fmt.Errorf("unsupported type %s", v.Type())
	}
	return nil
}
