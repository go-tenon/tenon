package rest

import (
	"reflect"
	"testing"
)

type rejectInput struct {
	ID   string `path:"id"`
	Body struct {
		Reason string `json:"reason"`
	}
}

func TestSchemaNamesAnonymousBody(t *testing.T) {
	n := &schemaNames{anon: map[reflect.Type]string{}}
	n.bodies(reflect.TypeFor[*rejectInput]())
	body := reflect.TypeFor[rejectInput]().Field(1).Type
	if got := n.name(body, "hint"); got != "RejectInputBody" {
		t.Errorf("anonymous body = %q", got)
	}
	if got := n.name(reflect.TypeFor[struct{ X int }](), "otherHint"); got != "OtherHint" {
		t.Errorf("unregistered anonymous = %q", got)
	}
}
