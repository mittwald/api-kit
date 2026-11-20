package engine

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"slices"
	"strings"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/expr"
)

// headers returns the headers of the request of a setter, nil for none. A
// header that needs a value nobody gave, such as one of a parameter that
// was left out, and one whose value comes to nothing, is left out. The
// values are secrets: they carry what a token would.
func (r *callRun) headers(setter *definition.Setter, vals values) http.Header {
	if len(setter.Headers) == 0 {
		return nil
	}
	header := http.Header{}
	for _, pair := range setter.Headers {
		lacking := slices.ContainsFunc(expr.ParseTemplate(pair.Value).Placeholders(), func(placeholder expr.Placeholder) bool {
			_, ok := vals.scalar(placeholder.Name)
			return placeholder.Braced && !ok
		})
		value := strings.TrimSpace(renderWith(pair.Value, vals.scalar))
		if lacking || value == "" {
			continue
		}
		r.secrets = append(r.secrets, value)
		header.Add(pair.Key, value)
	}
	return header
}

// headerSecrets keeps out of errors the values that the headers of a setter
// have before its steps ran, such as one of a parameter: a step that fails
// may repeat one. The steps add theirs themselves.
func (r *callRun) headerSecrets(setter *definition.Setter, vals values) {
	for _, header := range setter.Headers {
		for _, placeholder := range expr.ParseTemplate(header.Value).Placeholders() {
			if value, ok := vals.scalar(placeholder.Name); ok && apikit.Text(value) != "" {
				r.secrets = append(r.secrets, apikit.Text(value))
			}
		}
	}
}

// multipartForm returns the payload of a setter as a form of type
// multipart/form-data, and the content type that says where its parts
// end. A field whose template is a parameter of type file and nothing else
// holds the content of each of its files; every other field holds the
// text of each of its values.
func (r *callRun) multipartForm(setter *definition.Setter, body any) ([]byte, string, error) {
	fields, ok := body.(*apikit.Object)
	template, _ := setter.PayloadTemplate.(*apikit.Object)
	if !ok || template == nil {
		return nil, "", apikit.Errorf(apikit.KindDefinition, "setter %s of %s sends a form, which needs a mapping as its payload", setter.Name, r.resource)
	}
	files := map[string]bool{}
	for _, parameter := range setter.Parameters {
		if parameter.Type == definition.TypeFile {
			files[variableKey(parameter.Name)] = true
		}
	}

	var data bytes.Buffer
	form := multipart.NewWriter(&data)
	for key, value := range fields.All() {
		source, _ := template.Get(key)
		name, sole := expr.ParseTemplate(apikit.Text(source)).Sole()
		for _, item := range listOf(value) {
			var err error
			if sole && files[variableKey(name)] {
				err = addFile(form, key, apikit.Text(item))
			} else {
				err = form.WriteField(key, apikit.Text(item))
			}
			if err != nil {
				return nil, "", apikit.Errorf(apikit.KindFailed, "%s cannot send its form: %v", r.call.Name, err)
			}
		}
	}
	if err := form.Close(); err != nil {
		return nil, "", apikit.Errorf(apikit.KindFailed, "%s cannot send its form: %v", r.call.Name, err)
	}
	return data.Bytes(), form.FormDataContentType(), nil
}

// addFile adds a file to a form, under its base name and with the content
// type its extension, or else its content, suggests.
func addFile(form *multipart.Writer, field, path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%s cannot be read", path)
	}
	kind := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if kind == "" {
		kind = http.DetectContentType(content)
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", multipart.FileContentDisposition(field, filepath.Base(path)))
	header.Set("Content-Type", kind)
	part, err := form.CreatePart(header)
	if err != nil {
		return err
	}
	_, err = io.Copy(part, bytes.NewReader(content))
	return err
}
