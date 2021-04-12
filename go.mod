module github.com/lbryio/ytsync/v5

replace github.com/btcsuite/btcd => github.com/lbryio/lbrycrd.go v0.0.0-20200203050410-e1076f12bf19

//replace github.com/lbryio/lbry.go/v2 => /home/niko/go/src/github.com/lbryio/lbry.go/
//replace github.com/lbryio/reflector.go => /home/niko/go/src/github.com/lbryio/reflector.go/

require (
	github.com/Microsoft/go-winio v0.4.14 // indirect
	github.com/abadojack/whatlanggo v1.0.1
	github.com/asaskevich/govalidator v0.0.0-20200819183940-29e1ff8eb0bb
	github.com/aws/aws-sdk-go v1.25.9
	github.com/davecgh/go-spew v1.1.1
	github.com/docker/distribution v2.7.1+incompatible // indirect
	github.com/docker/docker v1.13.1
	github.com/docker/go-connections v0.4.0 // indirect
	github.com/docker/go-units v0.4.0 // indirect
	github.com/hashicorp/go-immutable-radix v1.1.0 // indirect
	github.com/hashicorp/go-sockaddr v1.0.2 // indirect
	github.com/hashicorp/memberlist v0.1.5 // indirect
	github.com/hashicorp/serf v0.8.5 // indirect
	github.com/kr/pretty v0.2.1 // indirect
	github.com/lbryio/lbry.go/v2 v2.7.2-0.20210412222918-ed51ece75c3d
	github.com/lbryio/reflector.go v1.1.3-0.20210412225256-4392c9724262
	github.com/miekg/dns v1.1.22 // indirect
	github.com/mitchellh/go-ps v0.0.0-20190716172923-621e5597135b
	github.com/opencontainers/go-digest v1.0.0-rc1 // indirect
	github.com/phayes/freeport v0.0.0-20180830031419-95f893ade6f2 // indirect
	github.com/prometheus/client_golang v0.9.3
	github.com/shopspring/decimal v0.0.0-20191009025716-f1972eb1d1f5
	github.com/sirupsen/logrus v1.4.2
	github.com/spf13/cobra v0.0.5
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/stretchr/testify v1.7.0
	google.golang.org/appengine v1.6.5 // indirect
	gopkg.in/ini.v1 v1.60.2 // indirect
	gopkg.in/vansante/go-ffprobe.v2 v2.0.2
	gotest.tools v2.2.0+incompatible
)

go 1.13
