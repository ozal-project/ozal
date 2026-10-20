package config

import (
	"reflect"
	"testing"
)

func TestRoutePolicy(t *testing.T) {
	configured := AccessPolicy{AllowedDomains: []string{"example.com"}, RequiredGroups: []string{"ops"}}
	tests := []struct {
		name  string
		route Route
		want  AccessPolicy
	}{
		{"configured access", Route{Access: &configured}, configured},
		{"empty access", Route{Access: &AccessPolicy{}}, AccessPolicy{}},
		{"legacy auth_required", Route{AuthRequired: true}, AccessPolicy{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.route.Policy(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Policy() = %+v; want %+v", got, tt.want)
			}
		})
	}
}
