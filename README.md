# go-tools
go工具脚本


# protoc-gen-api-registry

A protoc plugin that extracts gRPC-Gateway API metadata into a Go file containing `ApiList`, useful for permission control and frontend menu rendering.

## Features

- Extracts: path, method, service, group
- Supports `google.api.http` annotations
- Supports custom `white_list` tag

## Usage

1. Define your `.proto` file with annotations:

```proto
import "google/api/annotations.proto";
import "annotations/extra.proto";

rpc Login(LoginReq) returns (LoginResp) {
  option (google.api.http) = { post: "/v1/login", body: "*" };
  option (annotations.white_list) = true;
}