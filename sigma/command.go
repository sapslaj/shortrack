package sigma

import (
	"context"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/urfave/cli/v3"
	"google.golang.org/grpc"

	"github.com/sapslaj/shortrack/pb"
	"github.com/sapslaj/shortrack/pkg/telemetry"
)

func Command() *cli.Command {
	return &cli.Command{
		Name: "sigma",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name: "base-iqn",
			},
			&cli.StringFlag{
				Name:     "volume-dir",
				Required: true,
			},
			&cli.StringFlag{
				Name:  "portal",
				Value: "0.0.0.0:3260",
			},
			&cli.StringFlag{
				Name:  "listen",
				Value: ":29581",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			logger := telemetry.NewLogger()

			config := ServerConfig{
				BaseIQN:    cmd.String("base-iqn"),
				VolumesDir: cmd.String("volume-dir"),
				Portal:     cmd.String("portal"),
			}
			if config.BaseIQN == "" {
				hostname, err := os.Hostname()
				if err != nil {
					return err
				}
				config.BaseIQN = "iqn.2003-01.xyz.sapslaj.shortrack." + hostname
			}

			server := &Server{
				Config: config,
			}
			logger.InfoContext(
				ctx,
				"configured server",
				slog.String("iqn", config.BaseIQN),
				slog.String("volumes_dir", config.VolumesDir),
				slog.String("portal", config.Portal),
				slog.String("listen", cmd.String("listen")),
			)

			logger.InfoContext(ctx, "performing initial reconcile")
			err := server.LoadState(ctx)
			if err != nil {
				logger.ErrorContext(ctx, "error loading initial state", telemetry.Error(err))
				return err
			}

			err = server.ReconcileAll(ctx)
			if err != nil {
				logger.ErrorContext(ctx, "error during initial reconcile", telemetry.Error(err))
				return err
			}

			err = server.SaveState(ctx)
			if err != nil {
				logger.ErrorContext(ctx, "error saving state", telemetry.Error(err))
				return err
			}

			logger.InfoContext(ctx, "starting gRPC server")
			listener, err := net.Listen("tcp", cmd.String("listen"))
			if err != nil {
				return err
			}

			loggingOpts := []logging.Option{
				logging.WithLogOnEvents(logging.StartCall, logging.FinishCall),
			}
			interceptorLogger := logging.LoggerFunc(func(
				ctx context.Context,
				level logging.Level,
				msg string,
				fields ...any,
			) {
				telemetry.LoggerFromContext(ctx).Log(ctx, slog.Level(level), msg, fields...)
			})

			gs := grpc.NewServer(
				grpc.ChainUnaryInterceptor(
					logging.UnaryServerInterceptor(interceptorLogger, loggingOpts...),
				),
				grpc.ChainStreamInterceptor(
					logging.StreamServerInterceptor(interceptorLogger, loggingOpts...),
				),
			)
			pb.RegisterSigmaServer(gs, server)

			go func() {
				for range time.Tick(time.Second) {
					ctx := context.Background()
					err := server.ReconcileAll(ctx)
					if err != nil {
						logger.ErrorContext(ctx, "error during background reconcile", telemetry.Error(err))
					}
				}
			}()
			err = gs.Serve(listener)
			if err != nil {
				return err
			}

			return nil
		},
	}
}
