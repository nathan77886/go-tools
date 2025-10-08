package main

import (
	"bytes"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"text/template"
	"unicode"

	"google.golang.org/protobuf/proto"
	descriptorpb "google.golang.org/protobuf/types/descriptorpb"
	plugin "google.golang.org/protobuf/types/pluginpb"

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

func generateFile(file *descriptorpb.FileDescriptorProto) (string, error) {
	tpl := `package {{.Package}}

import "github.com/kataras/iris/v12"

// ==== Messages ====
{{range .Messages}}
type {{.Name}} struct {
{{- range .Fields}}
	{{.Name}} {{.Type}} ` + "`json:\"{{.JsonName}}\"`" + `
{{- end}}
}
{{end}}

// ==== Handlers ====
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

	type Field struct {
		Name     string
		Type     string
		JsonName string
	}

	type Message struct {
		Name   string
		Fields []Field
	}

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

	var messages []Message
	for _, msg := range file.GetMessageType() {
		var fields []Field
		for _, f := range msg.GetField() {
			fields = append(fields, Field{
				Name:     toCamelCase(f.GetName()),
				Type:     protoTypeToGo(f.GetType(), f.GetTypeName()),
				JsonName: f.GetName(),
			})
		}
		messages = append(messages, Message{
			Name:   msg.GetName(),
			Fields: fields,
		})
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
		Messages []Message
		Services []Service
	}{
		Package:  file.GetPackage(),
		Messages: messages,
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

// proto 类型 -> Go 类型映射
func protoTypeToGo(t descriptorpb.FieldDescriptorProto_Type, typeName string) string {
	switch t {
	case descriptorpb.FieldDescriptorProto_TYPE_DOUBLE:
		return "float64"
	case descriptorpb.FieldDescriptorProto_TYPE_FLOAT:
		return "float32"
	case descriptorpb.FieldDescriptorProto_TYPE_INT64,
		descriptorpb.FieldDescriptorProto_TYPE_SFIXED64,
		descriptorpb.FieldDescriptorProto_TYPE_SINT64:
		return "int64"
	case descriptorpb.FieldDescriptorProto_TYPE_UINT64,
		descriptorpb.FieldDescriptorProto_TYPE_FIXED64:
		return "uint64"
	case descriptorpb.FieldDescriptorProto_TYPE_INT32,
		descriptorpb.FieldDescriptorProto_TYPE_SFIXED32,
		descriptorpb.FieldDescriptorProto_TYPE_SINT32:
		return "int32"
	case descriptorpb.FieldDescriptorProto_TYPE_UINT32,
		descriptorpb.FieldDescriptorProto_TYPE_FIXED32:
		return "uint32"
	case descriptorpb.FieldDescriptorProto_TYPE_BOOL:
		return "bool"
	case descriptorpb.FieldDescriptorProto_TYPE_STRING:
		return "string"
	case descriptorpb.FieldDescriptorProto_TYPE_BYTES:
		return "[]byte"
	case descriptorpb.FieldDescriptorProto_TYPE_MESSAGE:
		return trimPackage(typeName)
	default:
		return "interface{}"
	}
}

// 去掉包前缀
func trimPackage(fullName string) string {
	if i := strings.LastIndex(fullName, "."); i != -1 {
		return fullName[i+1:]
	}
	return fullName
}

// 驼峰转换（下划线转首字母大写）
func toCamelCase(s string) string {
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if len(p) > 0 {
			r := []rune(p)
			r[0] = unicode.ToUpper(r[0])
			parts[i] = string(r)
		}
	}
	return strings.Join(parts, "")
}
