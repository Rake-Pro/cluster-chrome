module github.com/rake-pro/cluster-chrome

go 1.27.4

// No Multipath TCP listeners: nothing on the fleet dials MPTCP, and a listening
// MPTCP socket was the precondition of the CVE-2026-98432 host-kernel exploit.
godebug multipathtcp=0

require (
	github.com/rs/zerolog v1.35.1
	golang.org/x/net v0.62.0
	google.golang.org/grpc v1.85.0
	k8s.io/kubelet v0.37.2
)

require (
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260817212433-ac3dfec99bb1 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)
