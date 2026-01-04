module github.com/bililive-go/bililive-go

go 1.24.0

toolchain go1.24.7

require (
	github.com/Masterminds/sprig v2.22.0+incompatible
	github.com/alecthomas/kingpin v2.2.7-0.20180312062423-a39589180ebd+incompatible
	github.com/bluele/gcache v0.0.0-20190518031135-bc40bd653833
	github.com/golang-migrate/migrate/v4 v4.19.1
	github.com/gorilla/mux v1.7.4
	github.com/hr3lxphr6j/requests v0.0.1
	github.com/kira1928/remotetools v0.3.4
	github.com/prometheus/client_golang v1.11.0
	github.com/robertkrimen/otto v0.0.0-20191219234010-c382bd3c16ff
	github.com/satori/go.uuid v1.2.1-0.20181028125025-b2ce2384e17b
	github.com/sirupsen/logrus v1.9.3
	github.com/stretchr/testify v1.10.0
	github.com/tidwall/gjson v1.9.3
	go.uber.org/mock v0.5.2
	golang.org/x/sys v0.39.0
	gopkg.in/gomail.v2 v2.0.0-20160411212932-81ebce5c23df
	gopkg.in/yaml.v3 v3.0.1
	modernc.org/sqlite v1.42.2
)

replace github.com/kira1928/remotetools v0.3.4 => ../remotetools

require github.com/blang/semver/v4 v4.0.0 // indirect

require (
	github.com/Masterminds/goutils v1.1.0 // indirect
	github.com/Masterminds/semver v1.5.0 // indirect
	github.com/alecthomas/template v0.0.0-20190718012654-fb15b899a751 // indirect
	github.com/alecthomas/units v0.0.0-20190924025748-f65c72e2690d // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/golang/protobuf v1.5.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/huandu/xstrings v1.3.2 // indirect
	github.com/imdario/mergo v0.3.11 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.1 // indirect
	github.com/mitchellh/copystructure v1.0.0 // indirect
	github.com/mitchellh/reflectwalk v1.0.0 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/prometheus/client_model v0.2.0 // indirect
	github.com/prometheus/common v0.26.0 // indirect
	github.com/prometheus/procfs v0.6.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.0 // indirect
	github.com/ulikunitz/xz v0.5.15 // indirect
	golang.org/x/crypto v0.45.0 // indirect
	golang.org/x/exp v0.0.0-20251219203646-944ab1f22d93 // indirect
	google.golang.org/protobuf v1.36.7 // indirect
	gopkg.in/alexcesaro/quotedprintable.v3 v3.0.0-20150716171945-2caba252f4dc // indirect
	gopkg.in/sourcemap.v1 v1.0.5 // indirect
	modernc.org/libc v1.67.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)
