module SignTools

go 1.26.5

require (
	github.com/SherClockHolmes/webpush-go v1.4.0
	github.com/ViRb3/koanf-extra v0.0.0-20241224160111-fad8e9827c5f
	github.com/ViRb3/sling/v2 v2.0.2
	github.com/aixiansheng/lzfse v0.2.4
	github.com/byteness/keyring v1.9.0
	github.com/elliotchance/orderedmap v1.8.0
	github.com/eventials/go-tus v0.0.0-20250612203642-7827b129cd4c
	github.com/galecore/xslog v0.0.0-20230717081035-da7669fe4648
	github.com/google/go-github/v33 v33.0.0
	github.com/google/uuid v1.6.0
	github.com/juju/persistent-cookiejar v1.0.0
	github.com/knadh/koanf v1.5.0
	github.com/labstack/echo/v4 v4.16.3
	github.com/labstack/gommon v0.5.0
	github.com/natefinch/atomic v1.0.1
	github.com/pkg/errors v0.9.1
	github.com/rs/zerolog v1.35.1
	github.com/schollz/progressbar/v3 v3.19.1
	github.com/stretchr/testify v1.12.1
	github.com/tus/tusd/v2 v2.10.1
	github.com/ziflex/lecho/v2 v2.5.2
	go.uber.org/atomic v1.12.0
	golang.org/x/crypto v0.58.0
	golang.org/x/exp v0.0.0-20261109185158-a52e3aef5920
	golang.org/x/oauth2 v0.38.0
	gopkg.in/yaml.v3 v3.0.1
	howett.net/plist v1.0.1
	software.sslmate.com/src/go-pkcs12 v0.7.4
)

require (
	github.com/VividCortex/ewma v1.2.0 // indirect
	github.com/acarl005/stripansi v0.0.0-20180116102854-5a71ef0e047d // indirect
	github.com/andybalholm/brotli v1.2.1 // indirect
	github.com/apex/log v1.9.0 // indirect
	github.com/blacktop/go-dwarf v1.0.14 // indirect
	github.com/blacktop/go-macho v1.1.282 // indirect
	github.com/blacktop/go-plist v1.0.2 // indirect
	github.com/bodgit/plumbing v1.3.0 // indirect
	github.com/bodgit/sevenzip v1.6.3 // indirect
	github.com/bodgit/windows v1.0.1 // indirect
	github.com/deploymenttheory/go-macos-pkg v0.3.0 // indirect
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/ebitengine/purego v0.10.2 // indirect
	github.com/fatih/color v1.19.0 // indirect
	github.com/go-compressions/lzfse v0.3.0 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/juju/go4 v0.0.0-20160222163258-40d72ab9641a // indirect
	github.com/klauspost/compress v1.19.2 // indirect
	github.com/majd/go-apfs v1.0.29 // indirect
	github.com/mattn/go-runewidth v0.0.16 // indirect
	github.com/mitchellh/colorstring v0.0.0-20190213212951-d06e56a500db // indirect
	github.com/pierrec/lz4/v4 v4.1.29 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/spf13/afero v1.15.0 // indirect
	github.com/stangelandcl/ppmd v0.1.0 // indirect
	github.com/ulikunitz/xz v0.5.17 // indirect
	github.com/vbauerster/mpb/v7 v7.5.3 // indirect
	github.com/xi2/xz v0.0.0-20171230120015-48954b6210f8 // indirect
	go4.org v0.0.0-20260112195520-a5071408f32f // indirect
	golang.org/x/term v0.47.0 // indirect
	gopkg.in/errgo.v1 v1.0.1 // indirect
	gopkg.in/retry.v1 v1.0.3 // indirect
)

replace (
	github.com/tus/tusd/v2 v2.10.1 => github.com/SignTools/tusd/v2 v2.0.0-20261105210929-99d33c50be0f
	software.sslmate.com/src/go-pkcs12 v0.7.4 => github.com/SignTools/go-pkcs12 v0.0.0-20261105210929-5457a6812520
)

require (
	github.com/DrWhax/unfairplay v0.0.0
	github.com/fatih/structs v1.1.0 // indirect
	github.com/fsnotify/fsnotify v1.10.1 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/google/go-querystring v1.2.0 // indirect
	github.com/jba/slog v0.0.0-20230403194657-e1c00ce43c8a // indirect
	github.com/majd/ipatool/v2 v2.0.0
	github.com/mattn/go-colorable v0.1.16 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	github.com/tus/lockfile v1.2.0 // indirect
	github.com/tus/tusd v1.13.0 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasttemplate v1.2.2 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/net v0.62.0 // indirect
	golang.org/x/sys v0.50.0 // indirect
	golang.org/x/text v0.44.0 // indirect
	golang.org/x/time v0.16.0 // indirect
)

replace github.com/majd/ipatool/v2 => ./third_party/ipatool

replace github.com/DrWhax/unfairplay => ./third_party/unfairplay

replace github.com/byteness/keyring => ./internal/memorykeyring
