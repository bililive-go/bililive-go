module github.com/bililive-go/bililive-go

go 1.23.0

// 暂时注释掉，不然本地运行 `golangci-lint-v2.exe run --path-mode=abs --build-tags=dev` 会报错：
// The command is terminated due to an error: can't load config: the Go language version (go1.23) used to build golangci-lint is lower than the targeted Go version (1.24.4)
// toolchain go1.24.4

require (
	github.com/Masterminds/sprig v2.22.0+incompatible
	github.com/alecthomas/kingpin v2.2.7-0.20180312062423-a39589180ebd+incompatible
	github.com/bluele/gcache v0.0.0-20190518031135-bc40bd653833
	github.com/gorilla/mux v1.7.4
	github.com/hr3lxphr6j/requests v0.0.1
	github.com/lthibault/jitterbug v2.0.0+incompatible
	github.com/prometheus/client_golang v1.11.0
	github.com/robertkrimen/otto v0.0.0-20191219234010-c382bd3c16ff
	github.com/satori/go.uuid v1.2.1-0.20181028125025-b2ce2384e17b
	github.com/sirupsen/logrus v1.6.0
	github.com/stretchr/testify v1.9.0
	github.com/tidwall/gjson v1.9.3
	go.uber.org/mock v0.5.2
	gopkg.in/yaml.v2 v2.3.0
)

require (
	github.com/Masterminds/goutils v1.1.0 // indirect
	github.com/Masterminds/semver v1.5.0 // indirect
	github.com/alecthomas/template v0.0.0-20190718012654-fb15b899a751 // indirect
	github.com/alecthomas/units v0.0.0-20190924025748-f65c72e2690d // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.1.1 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/golang/protobuf v1.4.3 // indirect
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/google/uuid v1.1.2 // indirect
	github.com/huandu/xstrings v1.3.2 // indirect
	github.com/imdario/mergo v0.3.11 // indirect
	github.com/konsorten/go-windows-terminal-sequences v1.0.3 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.1 // indirect
	github.com/mitchellh/copystructure v1.0.0 // indirect
	github.com/mitchellh/reflectwalk v1.0.0 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/prometheus/client_model v0.2.0 // indirect
	github.com/prometheus/common v0.26.0 // indirect
	github.com/prometheus/procfs v0.6.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.0 // indirect
	golang.org/x/crypto v0.39.0 // indirect
	golang.org/x/sys v0.33.0 // indirect
	google.golang.org/protobuf v1.26.0-rc.1 // indirect
	gopkg.in/sourcemap.v1 v1.0.5 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
