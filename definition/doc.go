// Package definition reads the declarative description of an API that the
// rest of api-kit works from.
//
// A definition is a YAML document. It names the environments of the API and
// their base URLs, and describes its resources: where they live, which
// columns to show, how they relate to each other, and the setters that
// change them. A tool embeds its definition and loads it with [Load]; users
// can extend it with overlays of the same shape.
//
//	environments:
//	  production:
//	    apis:
//	      main: https://api.example.test/v1
//	resources:
//	  pets:
//	    columns: [id, name, species]
//	    filters: ["species="]
//	    setters:
//	      - name: rename
//	        method: PATCH
//	        pathTemplate: pets/${id}
//	        payloadTemplate: {name: "${name}"}
//	        parameters:
//	          - {name: name, help: New name of the pet}
//
// [Load] checks the whole document and reports every mistake at once, each
// with the path of the key it is about. Nothing here prints or exits: the
// mistakes come back as an error, and what was merely ignored as warnings.
//
// Regular expressions in a definition use the RE2 syntax of package regexp.
package definition
