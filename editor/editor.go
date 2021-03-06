package editor

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/hacdias/caddy-hugo/config"
	"github.com/hacdias/caddy-hugo/frontmatter"
	"github.com/hacdias/caddy-hugo/utils"
	"github.com/spf13/hugo/parser"
)

type editor struct {
	Name        string
	Class       string
	Mode        string
	Content     string
	FrontMatter interface{}
	Config      *config.Config
}

// ServeHTTP serves the editor page
func ServeHTTP(w http.ResponseWriter, r *http.Request, c *config.Config) (int, error) {
	filename := strings.Replace(r.URL.Path, "/admin/edit/", "", 1)

	if r.Method == "POST" {
		return servePost(w, r, filename)
	}

	return serveGet(w, r, c, filename)
}

func servePost(w http.ResponseWriter, r *http.Request, filename string) (int, error) {
	// Get the JSON information sent using a buffer
	rawBuffer := new(bytes.Buffer)
	rawBuffer.ReadFrom(r.Body)

	// Creates the raw file "map" using the JSON
	var rawFile map[string]interface{}
	json.Unmarshal(rawBuffer.Bytes(), &rawFile)

	// Initializes the file content to write
	var file []byte

	switch r.Header.Get("X-Content-Type") {
	case "frontmatter-only":
		frontmatter := strings.TrimPrefix(filepath.Ext(filename), ".")
		var mark rune

		switch frontmatter {
		case "toml":
			mark = rune('+')
		case "json":
			mark = rune('{')
		case "yaml":
			mark = rune('-')
		default:
			return 400, nil
		}

		f, err := parser.InterfaceToFrontMatter(rawFile, mark)
		fString := string(f)

		// If it's toml or yaml, strip frontmatter identifier
		if frontmatter == "toml" {
			fString = strings.TrimSuffix(fString, "+++\n")
			fString = strings.TrimPrefix(fString, "+++\n")
		}

		if frontmatter == "yaml" {
			fString = strings.TrimSuffix(fString, "---\n")
			fString = strings.TrimPrefix(fString, "---\n")
		}

		f = []byte(fString)

		if err != nil {
			w.Write([]byte(err.Error()))
			return 500, err
		}

		file = f
	case "content-only":
		// The main content of the file
		mainContent := rawFile["content"].(string)
		mainContent = "\n\n" + strings.TrimSpace(mainContent)

		file = []byte(mainContent)
	case "complete":
		// The main content of the file
		mainContent := rawFile["content"].(string)
		mainContent = "\n\n" + strings.TrimSpace(mainContent)

		// Removes the main content from the rest of the frontmatter
		delete(rawFile, "content")

		// Converts the frontmatter in JSON
		jsonFrontmatter, err := json.Marshal(rawFile)

		if err != nil {
			w.Write([]byte(err.Error()))
			return 500, err
		}

		// Indents the json
		frontMatterBuffer := new(bytes.Buffer)
		json.Indent(frontMatterBuffer, jsonFrontmatter, "", "  ")

		// Generates the final file
		f := new(bytes.Buffer)
		f.Write(frontMatterBuffer.Bytes())
		f.Write([]byte(mainContent))
		file = f.Bytes()
	default:
		return 400, nil
	}

	// Write the file
	err := ioutil.WriteFile(filename, file, 0666)

	if err != nil {
		w.Write([]byte(err.Error()))
		return 500, err
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte("{}"))
	return 200, nil
}

func serveGet(w http.ResponseWriter, r *http.Request, c *config.Config, filename string) (int, error) {
	// Check if the file format is supported. If not, send a "Not Acceptable"
	// header and an error
	if !utils.CanBeEdited(filename) {
		return 406, errors.New("File format not supported.")
	}

	// Check if the file exists. If it doesn't, send a "Not Found" message
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		w.Write([]byte(err.Error()))
		return 404, nil
	}

	// Open the file and check if there was some error while opening
	file, err := ioutil.ReadFile(filename)
	if err != nil {
		w.Write([]byte(err.Error()))
		return 500, err
	}

	// Create a new editor variable and set the extension
	page := new(editor)
	page.Mode = strings.TrimPrefix(filepath.Ext(filename), ".")
	page.Name = filename
	page.Config = c

	// Sanitize the extension
	page.Mode = sanitizeMode(page.Mode)

	// Handle the content depending on the file extension
	switch page.Mode {
	case "markdown":
		if hasFrontMatterRune(file) {
			// Starts a new buffer and parses the file using Hugo's functions
			buffer := bytes.NewBuffer(file)
			file, err := parser.ReadFrom(buffer)
			if err != nil {
				w.Write([]byte(err.Error()))
				return 500, err
			}

			// Parses the page content and the frontmatter
			page.Content = strings.TrimSpace(string(file.Content()))
			page.FrontMatter, err = frontmatter.Pretty(file.FrontMatter())
			page.Class = "complete"
		} else {
			// The editor will handle only content
			page.Class = "content-only"
			page.Content = string(file)
		}
	case "json", "toml", "yaml":
		// Defines the class and declares an error
		page.Class = "frontmatter-only"
		var err error

		// Checks if the file already has the frontmatter rune and parses it
		if hasFrontMatterRune(file) {
			page.FrontMatter, err = frontmatter.Pretty(file)
		} else {
			page.FrontMatter, err = frontmatter.Pretty(appendFrontMatterRune(file, page.Mode))
		}

		// Check if there were any errors
		if err != nil {
			w.Write([]byte(err.Error()))
			return 500, err
		}
	default:
		// The editor will handle only content
		page.Class = "content-only"
		page.Content = string(file)
	}

	// Create the functions map, then the template, check for erros and
	// execute the template if there aren't errors
	functions := template.FuncMap{
		"SplitCapitalize": utils.SplitCapitalize,
		"Defined":         utils.Defined,
	}

	tpl, err := utils.GetTemplate(r, functions, "editor", "frontmatter")

	if err != nil {
		w.Write([]byte(err.Error()))
		return 500, err
	}

	return 200, tpl.Execute(w, page)
}

func hasFrontMatterRune(file []byte) bool {
	return strings.HasPrefix(string(file), "---") ||
		strings.HasPrefix(string(file), "+++") ||
		strings.HasPrefix(string(file), "{")
}

func appendFrontMatterRune(frontmatter []byte, language string) []byte {
	switch language {
	case "yaml":
		return []byte("---\n" + string(frontmatter) + "\n---")
	case "toml":
		return []byte("+++\n" + string(frontmatter) + "\n+++")
	case "json":
		return frontmatter
	}

	return frontmatter
}

func sanitizeMode(extension string) string {
	switch extension {
	case "markdown", "md":
		return "markdown"
	case "css", "scss":
		return "css"
	case "html":
		return "htmlmixed"
	case "js":
		return "javascript"
	default:
		return extension
	}
}
