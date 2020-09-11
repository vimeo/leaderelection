package legrpc

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/vimeo/go-clocks/fake"

	"github.com/vimeo/leaderelection"
	"github.com/vimeo/leaderelection/entry"
	"github.com/vimeo/leaderelection/legrpc/testservice"
	"github.com/vimeo/leaderelection/memory"
)

type testServiceImpl struct{}

func (t *testServiceImpl) Send(ctx context.Context, req *testservice.Request) (*testservice.Response, error) {
	return &testservice.Response{
		Hello: req.Hello,
	}, nil
}

// partially copied from the ServiceConfig doc
// https://github.com/grpc/grpc/blob/master/doc/service_config.md#example
const unsimpleSC = `
{
  "methodConfig": [
    {
      "name": [
        { "service": "foo", "method": "bar" },
        { "service": "baz" }
      ],
      "timeout": "10.000000000s"
    }
  ]
}
`

func TestGRPCResolver(t *testing.T) {
	for _, itbl := range []struct {
		name       string
		ts         testservice.TestServiceServer
		connParams []byte
		checkConn  func(t testing.TB, conn *grpc.ClientConn)
	}{
		{
			name:       "simple",
			ts:         &testServiceImpl{},
			connParams: nil,
		},
		{
			name:       "withMethodConfig",
			ts:         &testServiceImpl{},
			connParams: []byte(unsimpleSC),
			checkConn: func(t testing.TB, conn *grpc.ClientConn) {
				mc := conn.GetMethodConfig("/foo/bar")
				if mc.Timeout == nil {
					t.Error("no timeout set for /foo/bar")
					return
				}
				if *mc.Timeout != time.Second*10 {
					t.Errorf("unexpected timeout for /foo/bar: %s; expected %s",
						*mc.Timeout, 10*time.Second)
				}
			},
		},
	} {
		tbl := itbl
		t.Run(tbl.name, func(t *testing.T) {
			t.Parallel()
			wg := sync.WaitGroup{}
			defer wg.Wait()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
			defer cancel()

			baseTime := time.Now()
			fc := fake.NewClock(baseTime)
			d := memory.NewDecider()

			l, listenErr := net.Listen("tcp", "localhost:0")
			if listenErr != nil {
				t.Fatalf("failed to create listener: %s", listenErr)
			}

			gsrv := grpc.NewServer()

			testservice.RegisterTestServiceServer(gsrv, tbl.ts)

			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := gsrv.Serve(l); err != nil {
					t.Errorf("ungraceful exit of grpc server: %s", err)
				}
			}()
			defer gsrv.GracefulStop()

			grpcAddr := l.Addr().String()

			leaderConf := leaderelection.Config{
				OnElected:        func(context.Context, *leaderelection.TimeView) {},
				OnOusting:        func(context.Context) {},
				LeaderChanged:    func(context.Context, entry.RaceEntry) {},
				LeaderID:         "bimbat",
				HostPort:         grpcAddr,
				Decider:          d,
				TermLength:       time.Minute * 10,
				MaxClockSkew:     time.Second,
				ConnectionParams: tbl.connParams,
				Clock:            fc,
			}

			wg.Add(1)
			go func() {
				defer wg.Done()
				aqErr := leaderConf.Acquire(ctx)
				if aqErr != nil && aqErr != context.Canceled {
					t.Errorf("acquisition failed: %s", aqErr)
				}
			}()

			rb := NewResolverBuilder(d, fc)

			conn, dialErr := grpc.DialContext(ctx, "leaderelection://memory/", grpc.WithResolvers(rb), grpc.WithBlock(), grpc.WithInsecure())
			if dialErr != nil {
				t.Fatalf("failed to dial request: %s", dialErr)
			}
			defer conn.Close()

			tsClient := testservice.NewTestServiceClient(conn)

			const helloVal = "foobarbaz"
			resp, sendErr := tsClient.Send(ctx, &testservice.Request{Hello: helloVal})
			if sendErr != nil {
				t.Errorf("failed to send request: %s", sendErr)
			}
			if resp.Hello != helloVal {
				t.Errorf("unexpected response hello: got %q; want %q ", resp.Hello, helloVal)
			}
			if tbl.checkConn != nil {
				tbl.checkConn(t, conn)
			}
		})
	}
}
