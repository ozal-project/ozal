package config

import (
	"reflect"
	"testing"
)

// TestCloneSharesNothing fills every pointer and slice reachable from Config
// by reflection, clones it, and checks that no pointer or slice backing array
// is shared. A field added to Config later is covered without editing this
// test: if Clone forgets to copy it, a mutation through the clone would leak
// into the live snapshot, and this fails.
func TestCloneSharesNothing(t *testing.T) {
	var source Config
	fill(reflect.ValueOf(&source).Elem())

	clone := Clone(source)
	if !reflect.DeepEqual(source, clone) {
		t.Fatal("clone differs from its source")
	}
	shared(t, "Config", reflect.ValueOf(source), reflect.ValueOf(clone))
}

// fill gives every nil pointer a new value and every slice one element,
// recursively, so each reference field has something to alias.
func fill(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		fill(v.Elem())
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				fill(v.Field(i))
			}
		}
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		fill(v.Index(0))
	case reflect.Map:
		v.Set(reflect.MakeMap(v.Type()))
	case reflect.String:
		v.SetString("x")
	}
}

func shared(t *testing.T, path string, a, b reflect.Value) {
	t.Helper()
	switch a.Kind() {
	case reflect.Pointer:
		if !a.IsNil() && a.Pointer() == b.Pointer() {
			t.Errorf("%s is shared between source and clone", path)
			return
		}
		if !a.IsNil() {
			shared(t, path, a.Elem(), b.Elem())
		}
	case reflect.Struct:
		for i := range a.NumField() {
			if a.Type().Field(i).IsExported() {
				shared(t, path+"."+a.Type().Field(i).Name, a.Field(i), b.Field(i))
			}
		}
	case reflect.Slice:
		if a.Len() > 0 && a.Pointer() == b.Pointer() {
			t.Errorf("%s shares its backing array", path)
			return
		}
		for i := range a.Len() {
			shared(t, path+"[]", a.Index(i), b.Index(i))
		}
	case reflect.Map:
		if !a.IsNil() && a.Pointer() == b.Pointer() {
			t.Errorf("%s is a shared map", path)
		}
	}
}
