package templates

import (
	"encoding/json"
	"testing"
)

const launchTemplate = `[
 {"type":"header","format":"image","example_parameters":[{"type":"image","url":"https://cdn.example.com/a.jpg"}]},
 {"type":"body","text":"Hi {{1}}, our {{2}} collection is here. {{3}}","example_parameters":[{"type":"text","text":"Sara"},{"type":"text","text":"Winter"},{"type":"text","text":"20% off"}]},
 {"type":"footer","text":"Reply STOP to unsubscribe"},
 {"type":"buttons","buttons":[
   {"type":"url","text":"Shop Now","url":"https://campaigns.example.com/r/{{1}}","example_parameters":[{"type":"text","text":"1/abc"}]},
   {"type":"quick_reply","text":"Stop promotions"}
 ]}
]`

func TestParseAndBuild(t *testing.T) {
	spec, err := Parse(json.RawMessage(launchTemplate))
	if err != nil {
		t.Fatal(err)
	}
	if spec.HeaderFormat != "image" || len(spec.BodyParams) != 3 || len(spec.Buttons) != 2 || !spec.Buttons[0].Tracked {
		t.Fatalf("unexpected spec: %+v", spec)
	}
	v := Variables{
		HeaderMediaURL: "https://cdn.example.com/winter.jpg",
		Body:           []string{"{first_name}", "Winter", "20% off"},
		ShopURL:        "https://shop.example.com/winter",
	}
	if err := spec.Validate(v); err != nil {
		t.Fatal(err)
	}
	comps := spec.Build(v, "", "Customer", "7/tok")
	if len(comps) != 3 {
		t.Fatalf("want header, body, one button; got %+v", comps)
	}
	if comps[0].Parameters[0].URL != v.HeaderMediaURL || comps[1].Parameters[0].Text != "Customer" || comps[2].Parameters[0].Text != "7/tok" {
		t.Fatalf("bad components: %+v", comps)
	}
	v.Body[2] = ""
	if spec.Validate(v) == nil {
		t.Fatal("empty body value accepted")
	}
}
