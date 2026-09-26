package discovery

import (
	"reflect"
	"testing"
)

// TestEqualInstanceCoversEveryField changes one field at a time, including
// the fields of Node, and requires equalInstance to notice every change. It
// fails when a field is added to ServiceInstance without being compared.
func TestEqualInstanceCoversEveryField(t *testing.T) {
	base := ServiceInstance{}
	if !equalInstance(base, base) {
		t.Fatal("an instance must equal itself")
	}
	typ := reflect.TypeFor[ServiceInstance]()
	for i := range typ.NumField() {
		f := typ.Field(i)
		if f.Type.Kind() == reflect.Struct && f.Type != reflect.TypeFor[Weights]() {
			for j := range f.Type.NumField() {
				other := base
				setNonZero(reflect.ValueOf(&other).Elem().Field(i).Field(j))
				if equalInstance(base, other) {
					t.Errorf("equalInstance ignores %s.%s", f.Name, f.Type.Field(j).Name)
				}
			}
			continue
		}
		other := base
		setNonZero(reflect.ValueOf(&other).Elem().Field(i))
		if equalInstance(base, other) {
			t.Errorf("equalInstance ignores %s", f.Name)
		}
	}
}

func TestEqualInstanceTreatsNilAndEmptyAlike(t *testing.T) {
	a := ServiceInstance{Tags: nil, Meta: nil}
	b := ServiceInstance{Tags: []string{}, Meta: map[string]string{}}
	if !equalInstance(a, b) {
		t.Fatal("nil and empty collections must compare equal")
	}
}

// setNonZero stores a value different from the zero value in v.
func setNonZero(v reflect.Value) {
	switch v.Kind() {
	case reflect.String:
		v.SetString("x")
	case reflect.Int:
		v.SetInt(1)
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 1, 1)
		setNonZero(s.Index(0))
		v.Set(s)
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		k := reflect.New(v.Type().Key()).Elem()
		e := reflect.New(v.Type().Elem()).Elem()
		setNonZero(k)
		setNonZero(e)
		m.SetMapIndex(k, e)
		v.Set(m)
	case reflect.Struct:
		setNonZero(v.Field(0))
	case reflect.Bool:
		v.SetBool(true)
	default:
		panic("setNonZero: unsupported kind " + v.Kind().String())
	}
}
