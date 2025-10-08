package main

import (
	"bytes"
	"google.golang.org/protobuf/proto"
	descriptorpb "google.golang.org/protobuf/types/descriptorpb"
	plugin "google.golang.org/protobuf/types/pluginpb"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"text/template"

	annotations "google.golang.org/genproto/googleapis/api/annotations"
)

func main() {
	input, err := ioutil.ReadAll(os.Stdin)
	if err != nil {
		log.Fatalf("failed to read input: %v", err)
	}

	req := &plugin.CodeGeneratorRequest{}
	if err := proto.Unmarshal(input, req); err != nil {
		log.Fatalf("failed to unmarshal request: %v", err)
	}

	resp := &plugin.CodeGeneratorResponse{}

	for _, file := range req.GetProtoFile() {
		content, err := generateFile(file)
		if err != nil {
			log.Fatalf("generate file failed: %v", err)
		}
		if content != "" {
			resp.File = append(resp.File, &plugin.CodeGeneratorResponse_File{
				Name:    proto.String(file.GetName() + "_iris.gen.go"),
				Content: proto.String(content),
			})
		}
	}

	out, err := proto.Marshal(resp)
	if err != nil {
		log.Fatalf("failed to marshal response: %v", err)
	}

	os.Stdout.Write(out)
}
func getGoPackage(file *descriptorpb.FileDescriptorProto) string {
	// 优先使用 go_package
	if gp := file.GetOptions().GetGoPackage(); gp != "" {
		parts := strings.Split(gp, "/")
		return parts[len(parts)-1]
	}
	// 否则使用 proto package
	if file.GetPackage() != "" {
		parts := strings.Split(file.GetPackage(), ".")
		return parts[len(parts)-1]
	}
	return "main"
}
func generateFile(file *descriptorpb.FileDescriptorProto) (string, error) {
	if len(file.GetService()) == 0 {
		// 没有 service，就不生成任何文件
		return "", nil
	}
	tpl := `package {{.Package}}

import "github.com/kataras/iris/v12"

{{range .Services}}
type {{.Name}}Handler interface {
{{- range .Methods}}
	{{.Name}}(ctx iris.Context, req *{{.InputType}}) (*{{.OutputType}}, error)
{{- end}}
}

func Register{{.Name}}(app *iris.Application, handler {{.Name}}Handler) {
{{- range .Methods}}
	{{- $path := .HttpPath }}
	{{- $method := .HttpMethod }}
	{{- if eq $method "GET" }}
app.Get("{{if $path}}{{$path}}{{else}}/{{.ParentName}}/{{.Name}}{{end}}", func(ctx iris.Context) {
{{- else if eq $method "PUT" }}
app.Put("{{if $path}}{{$path}}{{else}}/{{.ParentName}}/{{.Name}}{{end}}", func(ctx iris.Context) {
{{- else if eq $method "PATCH" }}
app.Patch("{{if $path}}{{$path}}{{else}}/{{.ParentName}}/{{.Name}}{{end}}", func(ctx iris.Context) {
{{- else if eq $method "DELETE" }}
app.Delete("{{if $path}}{{$path}}{{else}}/{{.ParentName}}/{{.Name}}{{end}}", func(ctx iris.Context) {
{{- else }}
app.Post("{{if $path}}{{$path}}{{else}}/{{.ParentName}}/{{.Name}}{{end}}", func(ctx iris.Context) {
{{- end }}

		var req {{.InputType}}
		if err := ctx.ReadJSON(&req); err != nil {
			ctx.StatusCode(400)
			ctx.JSON(map[string]interface{}{
				"code":  400,
				"data":  nil,
				"error": err.Error(),
			})
			return
		}

		resp, err := handler.{{.Name}}(ctx, &req)
		code := ctx.Values().GetIntDefault("code", 0)
		if err != nil {
			ctx.StatusCode(500)
			ctx.JSON(map[string]interface{}{
				"code":  code,
				"data":  nil,
				"error": err.Error(),
			})
			return
		}

		ctx.JSON(map[string]interface{}{
			"code":  code,
			"data":  resp,
			"error": nil,
		})
	})
{{- end}}
}
{{end}}
`

	type Method struct {
		Name       string
		InputType  string
		OutputType string
		ParentName string
		HttpPath   string
		HttpMethod string
	}

	type Service struct {
		Name    string
		Methods []Method
	}

	var services []Service
	for _, svc := range file.GetService() {
		var methods []Method
		for _, m := range svc.GetMethod() {
			httpPath, httpMethod := parseHttpOption(m)
			methods = append(methods, Method{
				Name:       m.GetName(),
				InputType:  trimPackage(m.GetInputType()),
				OutputType: trimPackage(m.GetOutputType()),
				ParentName: svc.GetName(),
				HttpPath:   httpPath,
				HttpMethod: httpMethod,
			})
		}
		services = append(services, Service{
			Name:    svc.GetName(),
			Methods: methods,
		})
	}

	data := struct {
		Package  string
		Services []Service
	}{
		Package:  getGoPackage(file),
		Services: services,
	}

	var buf bytes.Buffer
	t := template.Must(template.New("iris").Parse(tpl))
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// 解析 google.api.http option
func parseHttpOption(m *descriptorpb.MethodDescriptorProto) (path string, method string) {
	opts := m.GetOptions()
	if opts == nil {
		return "", ""
	}

	ext := proto.GetExtension(opts, annotations.E_Http)
	if ext == nil {
		return "", ""
	}

	httpRule, ok := ext.(*annotations.HttpRule)
	if !ok {
		return "", ""
	}

	switch httpRule.Pattern.(type) {
	case *annotations.HttpRule_Get:
		return httpRule.GetGet(), "GET"
	case *annotations.HttpRule_Post:
		return httpRule.GetPost(), "POST"
	case *annotations.HttpRule_Put:
		return httpRule.GetPut(), "PUT"
	case *annotations.HttpRule_Patch:
		return httpRule.GetPatch(), "PATCH"
	case *annotations.HttpRule_Delete:
		return httpRule.GetDelete(), "DELETE"
	case *annotations.HttpRule_Custom:
		return httpRule.GetCustom().GetPath(), httpRule.GetCustom().GetKind()
	default:
		return "", ""
	}
}

// 去掉包前缀
func trimPackage(fullName string) string {
	if i := strings.LastIndex(fullName, "."); i != -1 {
		return fullName[i+1:]
	}
	return fullName
}
