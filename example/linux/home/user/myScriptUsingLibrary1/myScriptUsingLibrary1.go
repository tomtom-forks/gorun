// User comments are left in the same place when go.mod and go.sum sections are updated

// go.mod >>>
// :module myScriptUsingLibrary1
// :go 1.18
// :require github.com/sirupsen/logrus v1.9.0
// :require golang.org/x/sys v0.0.0-20220715151400-c0bba94af5f8 // indirect
// <<< go.mod

// go.sum >>>
// :github.com/davecgh/go-spew v1.1.0/go.mod h1:J7Y8YcW2NihsgmVo/mv3lAwl/skON4iLHjSsI+c5H38=
// :github.com/davecgh/go-spew v1.1.1 h1:vj9j/u1bqnvCEfJOwUhtlOARqs3+rkHYY13jYWTU97c=
// :github.com/davecgh/go-spew v1.1.1/go.mod h1:J7Y8YcW2NihsgmVo/mv3lAwl/skON4iLHjSsI+c5H38=
// :github.com/pmezard/go-difflib v1.0.0 h1:4DBwDE0NGyQoBHbLQYPwSUPoCMWR5BEzIk/f1lZbAQM=
// :github.com/pmezard/go-difflib v1.0.0/go.mod h1:iKH77koFhYxTK1pcRnkKkqfTogsbg7gZNVY4sRDYZ/4=
// :github.com/sirupsen/logrus v1.9.0 h1:trlNQbNUG3OdDrDil03MCb1H2o9nJ1x4/5LYw7byDE0=
// :github.com/sirupsen/logrus v1.9.0/go.mod h1:naHLuLoDiP4jHNo9R0sCBMtWGeIprob74mVsIT4qYEQ=
// :github.com/stretchr/objx v0.1.0/go.mod h1:HFkY916IF+rwdDfMAkV7OtwuqBVzrE8GR6GFx+wExME=
// :github.com/stretchr/testify v1.7.0 h1:nwc3DEeHmmLAfoZucVR881uASk0Mfjw8xYJ99tb5CcY=
// :github.com/stretchr/testify v1.7.0/go.mod h1:6Fq8oRcR53rry900zMqJjRRixrwX3KX962/h/Wwjteg=
// :golang.org/x/sys v0.0.0-20220715151400-c0bba94af5f8 h1:0A+M6Uqn+Eje4kHMK80dtF3JCXC4ykBgQG4Fe06QRhQ=
// :golang.org/x/sys v0.0.0-20220715151400-c0bba94af5f8/go.mod h1:oPkhp1MJrh7nUepCBck5+mAzfO9JrbApNNgaTdGDITg=
// :gopkg.in/check.v1 v0.0.0-20161208181325-20d25e280405/go.mod h1:Co6ibVJAznAaIkqp8huTwlJQCZ016jof/cbN4VW5Yz0=
// :gopkg.in/yaml.v3 v3.0.0-20200313102051-9f266ea9e77c h1:dUUwHk2QECo/6vqA44rthZ8ie2QXMNeKRTHCNY2nXvo=
// :gopkg.in/yaml.v3 v3.0.0-20200313102051-9f266ea9e77c/go.mod h1:K4uyk7z7BCEPqu6E+C64Yfv1cQ7kz7rIZviUmN+EgEM=
// <<< go.sum

// go.work >>>
// :go 1.18
// :use (
// :	.
// :	../_goCommonLibs/exampleLibrary
// :)
// <<< go.work

// go.work.sum >>>
// :github.com/stretchr/objx v0.1.0 h1:4G4v2dO3VZwixGIRoQ5Lfboy6nUhCyYzaqnIAPPhYs4=
// :gopkg.in/check.v1 v0.0.0-20161208181325-20d25e280405 h1:yhCVgyC4o1eVCa2tZl7eS0r+SDo693bJlVdllGtEeKM=
// <<< go.work.sum

package main

import (
	// notice the exampleLibrary is available due to the go.work file, and the go.work is embedded in the comments
	// above, after running the Makefile.
	"exampleLibrary"
	"flag"
	. "myScriptUsingLibrary1/myScriptUsingLibrary1_"
	// Note how importing logrus and running the Makefile the go.mod/go.sum are updated then embedded as comments above
	log "github.com/sirupsen/logrus"
)

func main() {
	var port int
	flag.IntVar(&port, "port", 8083, "port to listen to https requests on")
	flag.Parse()

	port = exampleLibrary.Add1000(port)
	log.Printf("Starting on port %d", port)
	ProxyStart(port)
}
