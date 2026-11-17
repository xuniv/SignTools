//go:build (!amd64 && !arm64) || (!linux && !darwin && !windows)

package store

import "context"

type Service struct{}

func New() *Service { return &Service{} }

var Default API = New()

func GetInfo() Info {
	return Info{Available: false, Version: Version, Revision: Revision, Platforms: []string{}, Message: "The pinned ipatool runtime supports Linux, macOS and Windows on amd64/arm64. Other SignTools features remain available."}
}
func (*Service) Login(context.Context, LoginRequest) (LoginResponse, error) {
	return LoginResponse{}, ErrUnavailable
}
func (*Service) Execute(context.Context, string, Request) (Response, error) {
	return Response{}, ErrUnavailable
}
func (*Service) Download(context.Context, Request, Progress) (DownloadResult, error) {
	return DownloadResult{}, ErrUnavailable
}

func (*Service) Repair(context.Context, RepairRequest, Progress) (DownloadResult, error) {
	return DownloadResult{}, ErrUnavailable
}
