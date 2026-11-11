package engine

import (
	"encoding/base64"
	"strings"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/expr"
)

// linkKey is the key of the link to an item in the object the item keeps
// under [definition.ExtraKey].
const linkKey = "link"

// Link returns the link to an item in the web interface, "" when the
// resource has no link template.
//
// The link is the URL of the template, in which "${parameter}" stands for
// the parameter of the template, followed by the query parameters in the
// order of the definition. A URL without a host is appended to the link
// base of the environment.
//
// The parameter and the values of the query parameters are templates
// whose placeholders are paths into the item, so they reach the values of
// joined resources as well. A value that is missing, null or empty
// renders as nothing, and a query parameter whose value comes out empty
// is left out. Values are escaped as [RFC 3986] has it, a space as "%20".
// A parameter that is encoded is rendered from the values as they are and
// then written in base64.
//
// [RFC 3986]: https://www.rfc-editor.org/rfc/rfc3986
func (e *Engine) Link(resource string, item *apikit.Object) string {
	template := e.doc.Resource(resource).URLTemplate
	if template == nil || item.Len() == 0 {
		return ""
	}
	lookup := func(path string) (any, bool) {
		return at(item, path), true
	}

	link := template.URL
	if !strings.HasPrefix(link, "http://") && !strings.HasPrefix(link, "https://") {
		link = e.linkBase() + "/" + strings.TrimPrefix(link, "/")
	}
	if template.Parameter != "" {
		// The parameter is part of a path that the template spells out,
		// slashes included: only what the item puts into it is escaped.
		// Base64 needs no escaping, and must not encode one.
		options := expr.RenderOptions{Format: func(value any) string { return quote(apikit.Text(value)) }}
		if template.EncodeParameter {
			options.Format = nil
		}
		parameter, _ := expr.ParseTemplate(template.Parameter).Render(lookup, options)
		parameter = strings.TrimSpace(parameter)
		if template.EncodeParameter {
			parameter = base64.StdEncoding.EncodeToString([]byte(parameter))
		}
		link, _ = expr.ParseTemplate(link).Render(func(name string) (any, bool) {
			return parameter, name == "parameter"
		}, expr.RenderOptions{})
		link = strings.TrimSpace(link)
	}
	for _, query := range template.Query {
		value, _ := expr.ParseTemplate(query.Template).Render(lookup, expr.RenderOptions{})
		value = strings.TrimSpace(value)
		if query.Name == "" || value == "" {
			continue
		}
		separator := "?"
		if strings.Contains(link, "?") {
			separator = "&"
		}
		// The value is escaped as a whole, once: escaping what the item
		// puts into it first would escape the escapes again.
		link += separator + query.Name + "=" + quote(value)
	}
	return link
}

// linkBase returns what links without a host are appended to in the
// environment of the Engine.
func (e *Engine) linkBase() string {
	for _, environment := range e.doc.Environments {
		if e.environment == "" || environment.Name == e.environment {
			return environment.LinkBase
		}
	}
	return ""
}

// quote escapes text for a URL: ASCII letters and digits, "-", ".", "_",
// "~" and "/" stay as they are, every other byte becomes "%XX". Unlike
// [net/url.QueryEscape] it writes a space as "%20", which reads the same in a
// path and in a query, and unlike [net/url.PathEscape] it escapes the
// characters that end or divide a query.
func quote(text string) string {
	const digits = "0123456789ABCDEF"
	var b strings.Builder
	for i := range len(text) {
		c := text[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '.', c == '_', c == '~', c == '/':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(digits[c>>4])
			b.WriteByte(digits[c&0x0f])
		}
	}
	return b.String()
}
