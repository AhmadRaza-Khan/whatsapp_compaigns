// Package templates turns a Bird template's content into the list of values a
// campaign has to fill in, and builds the per-recipient send components.
package templates

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"wacampaigns/internal/bird"
)

type Param struct {
	Name    string `json:"name,omitempty"` // set for templates with named placeholders
	Example string `json:"example"`
}

type Button struct {
	Type    string  `json:"type"`
	Text    string  `json:"text"`
	URL     string  `json:"url,omitempty"`
	Params  []Param `json:"params,omitempty"`
	Tracked bool    `json:"tracked"` // URL routes through the platform's /r/ redirect
}

type Spec struct {
	HeaderFormat     string   `json:"header_format"` // "", text, image, video, gif, document, location
	HeaderText       string   `json:"header_text,omitempty"`
	HeaderParams     []Param  `json:"header_params,omitempty"`
	HeaderExampleURL string   `json:"header_example_url,omitempty"`
	Body             string   `json:"body"`
	BodyParams       []Param  `json:"body_params,omitempty"`
	Footer           string   `json:"footer,omitempty"`
	Buttons          []Button `json:"buttons,omitempty"`
}

// Variables are the values a campaign supplies for its template.
type Variables struct {
	HeaderMediaURL string     `json:"header_media_url"`
	HeaderText     []string   `json:"header_text"`
	Body           []string   `json:"body"`
	Buttons        [][]string `json:"buttons"`  // values for untracked buttons, by button index
	ShopURL        string     `json:"shop_url"` // where the tracked "Shop Now" button lands
}

type rawParam struct {
	Type string `json:"type"`
	Text string `json:"text"`
	URL  string `json:"url"`
	Name string `json:"name"`
}

type rawComponent struct {
	Type              string     `json:"type"`
	Format            string     `json:"format"`
	Text              string     `json:"text"`
	ExampleParameters []rawParam `json:"example_parameters"`
	Buttons           []struct {
		Type              string     `json:"type"`
		Text              string     `json:"text"`
		URL               string     `json:"url"`
		ExampleParameters []rawParam `json:"example_parameters"`
	} `json:"buttons"`
}

var placeholder = regexp.MustCompile(`\{\{\s*([\w]+)\s*\}\}`)

// params returns one Param per placeholder, preferring Bird's example list and
// falling back to the placeholders found in the text.
func params(text string, ex []rawParam) []Param {
	if len(ex) > 0 {
		out := make([]Param, len(ex))
		for i, p := range ex {
			out[i] = Param{Name: p.Name, Example: p.Text}
		}
		return out
	}
	var out []Param
	seen := map[string]bool{}
	for _, m := range placeholder.FindAllStringSubmatch(text, -1) {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		p := Param{}
		if !isDigits(m[1]) {
			p.Name = m[1]
		}
		out = append(out, p)
	}
	return out
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// Parse builds a Spec from the components JSON of a template language.
func Parse(components json.RawMessage) (Spec, error) {
	var spec Spec
	var comps []rawComponent
	if len(components) == 0 {
		return spec, nil
	}
	if err := json.Unmarshal(components, &comps); err != nil {
		return spec, err
	}
	for _, c := range comps {
		switch strings.ToLower(c.Type) {
		case "header":
			spec.HeaderFormat = strings.ToLower(c.Format)
			if spec.HeaderFormat == "" {
				spec.HeaderFormat = "text"
			}
			if spec.HeaderFormat == "text" {
				spec.HeaderText = c.Text
				spec.HeaderParams = params(c.Text, c.ExampleParameters)
			} else if len(c.ExampleParameters) > 0 {
				spec.HeaderExampleURL = c.ExampleParameters[0].URL
			}
		case "body":
			spec.Body = c.Text
			spec.BodyParams = params(c.Text, c.ExampleParameters)
		case "footer":
			spec.Footer = c.Text
		case "buttons":
			for _, b := range c.Buttons {
				btn := Button{Type: strings.ToLower(b.Type), Text: b.Text, URL: b.URL}
				if btn.Type == "url" {
					btn.Params = params(b.URL, b.ExampleParameters)
					btn.Tracked = len(btn.Params) == 1 && strings.Contains(b.URL, "/r/{{")
				} else if btn.Type == "copy_code" {
					btn.Params = params("", b.ExampleParameters)
				}
				spec.Buttons = append(spec.Buttons, btn)
			}
		}
	}
	return spec, nil
}

// HasTrackedButton reports whether clicks can be recorded for this template.
func (s Spec) HasTrackedButton() bool {
	for _, b := range s.Buttons {
		if b.Tracked {
			return true
		}
	}
	return false
}

// Validate checks that every placeholder has a value.
func (s Spec) Validate(v Variables) error {
	switch s.HeaderFormat {
	case "image", "video", "gif", "document":
		u, err := url.Parse(v.HeaderMediaURL)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return fmt.Errorf("header %s needs a public https URL", s.HeaderFormat)
		}
	case "location":
		return fmt.Errorf("location headers are not supported by this platform")
	}
	if err := checkValues("header", s.HeaderParams, v.HeaderText); err != nil {
		return err
	}
	if err := checkValues("body", s.BodyParams, v.Body); err != nil {
		return err
	}
	for i, b := range s.Buttons {
		if len(b.Params) == 0 {
			continue
		}
		if b.Tracked {
			u, err := url.Parse(v.ShopURL)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
				return fmt.Errorf("the %q button needs a shop link (https://...)", b.Text)
			}
			continue
		}
		var vals []string
		if i < len(v.Buttons) {
			vals = v.Buttons[i]
		}
		if err := checkValues(fmt.Sprintf("button %q", b.Text), b.Params, vals); err != nil {
			return err
		}
	}
	return nil
}

func checkValues(where string, ps []Param, vals []string) error {
	if len(vals) < len(ps) {
		return fmt.Errorf("%s: %d value(s) required, %d given", where, len(ps), len(vals))
	}
	for i := range ps {
		val := strings.TrimSpace(vals[i])
		if val == "" {
			return fmt.Errorf("%s: value %d is empty", where, i+1)
		}
		if strings.ContainsAny(val, "\n\t") {
			return fmt.Errorf("%s: value %d cannot contain line breaks or tabs", where, i+1)
		}
	}
	return nil
}

// Personalize replaces {name} and {first_name} with the contact's name.
func Personalize(val, name, fallback string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = fallback
	}
	first := name
	if f := strings.Fields(name); len(f) > 0 {
		first = f[0]
	}
	val = strings.ReplaceAll(val, "{first_name}", first)
	return strings.ReplaceAll(val, "{name}", name)
}

// Build creates the components for one recipient. trackingSuffix fills the
// tracked URL button, e.g. "12/k3j4h5" for https://site/r/{{1}}.
func (s Spec) Build(v Variables, contactName, nameFallback, trackingSuffix string) []bird.Component {
	var out []bird.Component
	textParams := func(ps []Param, vals []string) []bird.Param {
		res := make([]bird.Param, len(ps))
		for i, p := range ps {
			res[i] = bird.Param{Type: "text", Name: p.Name, Text: Personalize(strings.TrimSpace(vals[i]), contactName, nameFallback)}
		}
		return res
	}
	switch s.HeaderFormat {
	case "image", "video", "gif", "document":
		out = append(out, bird.Component{Type: "header", Parameters: []bird.Param{{Type: s.HeaderFormat, URL: v.HeaderMediaURL}}})
	case "text":
		if len(s.HeaderParams) > 0 {
			out = append(out, bird.Component{Type: "header", Parameters: textParams(s.HeaderParams, v.HeaderText)})
		}
	}
	if len(s.BodyParams) > 0 {
		out = append(out, bird.Component{Type: "body", Parameters: textParams(s.BodyParams, v.Body)})
	}
	// One "button" part per button that takes a value, in button order.
	for i, b := range s.Buttons {
		if len(b.Params) == 0 {
			continue
		}
		if b.Tracked {
			out = append(out, bird.Component{Type: "button", Parameters: []bird.Param{{Type: "text", Name: b.Params[0].Name, Text: trackingSuffix}}})
			continue
		}
		out = append(out, bird.Component{Type: "button", Parameters: textParams(b.Params, v.Buttons[i])})
	}
	return out
}
