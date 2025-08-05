package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/sapslaj/shortrack/k8sprovisioner"
	"github.com/sapslaj/shortrack/pb"
	"github.com/sapslaj/shortrack/pkg/ptr"
	"github.com/sapslaj/shortrack/pkg/telemetry"
	"github.com/sapslaj/shortrack/sigma"
)

func NewSigmaClient(ctx context.Context, addr string) (pb.SigmaClient, func() error, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}

	closer := func() error {
		return conn.Close()
	}
	return pb.NewSigmaClient(conn), closer, nil
}

func JSON(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err.Error()
	}
	return string(data)
}

func main() {
	cmd := &cli.Command{
		Commands: []*cli.Command{
			sigma.Command(),

			k8sprovisioner.Command(),

			{
				Name: "list-pools",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "addr",
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					c, closer, err := NewSigmaClient(ctx, cmd.String("addr"))
					if err != nil {
						return err
					}
					defer closer()

					res, err := c.ListPools(ctx, &pb.ListPoolsRequest{})
					if err != nil {
						return err
					}
					fmt.Println(JSON(res))
					return nil
				},
			},

			{
				Name: "get-pool",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "addr",
						Required: true,
					},
					&cli.Uint32Flag{
						Name: "pool-id",
					},
					&cli.StringFlag{
						Name: "pool-name",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					c, closer, err := NewSigmaClient(ctx, cmd.String("addr"))
					if err != nil {
						return err
					}
					defer closer()

					req := &pb.GetPoolRequest{}
					if cmd.Uint32("pool-id") != 0 {
						req.PoolId = ptr.Of(cmd.Uint32("pool-id"))
					}
					if cmd.String("pool-name") != "" {
						req.PoolName = ptr.Of(cmd.String("pool-name"))
					}
					res, err := c.GetPool(ctx, req)
					if err != nil {
						return err
					}
					fmt.Println(JSON(res))
					return nil
				},
			},

			{
				Name: "create-pool",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "addr",
						Required: true,
					},
					&cli.Uint32Flag{
						Name: "pool-id",
					},
					&cli.StringFlag{
						Name: "pool-name",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					c, closer, err := NewSigmaClient(ctx, cmd.String("addr"))
					if err != nil {
						return err
					}
					defer closer()

					req := &pb.CreatePoolRequest{}
					if cmd.Uint32("pool-id") != 0 {
						req.PoolId = ptr.Of(cmd.Uint32("pool-id"))
					}
					if cmd.String("pool-name") != "" {
						req.PoolName = ptr.Of(cmd.String("pool-name"))
					}
					res, err := c.CreatePool(ctx, req)
					if err != nil {
						return err
					}
					fmt.Println(JSON(res))
					return nil
				},
			},

			{
				Name: "delete-pool",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "addr",
						Required: true,
					},
					&cli.Uint32Flag{
						Name:     "pool-id",
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					c, closer, err := NewSigmaClient(ctx, cmd.String("addr"))
					if err != nil {
						return err
					}
					defer closer()

					res, err := c.DeletePool(ctx, &pb.DeletePoolRequest{
						PoolId: cmd.Uint32("pool-id"),
					})
					if err != nil {
						return err
					}
					fmt.Println(JSON(res))
					return nil
				},
			},

			{
				Name: "list-volumes",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "addr",
						Required: true,
					},
					&cli.Uint32Flag{
						Name:     "pool-id",
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					c, closer, err := NewSigmaClient(ctx, cmd.String("addr"))
					if err != nil {
						return err
					}
					defer closer()

					res, err := c.ListVolumes(ctx, &pb.ListVolumesRequest{
						PoolId: cmd.Uint32("pool-id"),
					})
					if err != nil {
						return err
					}
					fmt.Println(JSON(res))
					return nil
				},
			},

			{
				Name: "list-volumes",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "addr",
						Required: true,
					},
					&cli.Uint32Flag{
						Name:     "pool-id",
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					c, closer, err := NewSigmaClient(ctx, cmd.String("addr"))
					if err != nil {
						return err
					}
					defer closer()

					res, err := c.ListVolumes(ctx, &pb.ListVolumesRequest{
						PoolId: cmd.Uint32("pool-id"),
					})
					if err != nil {
						return err
					}
					fmt.Println(JSON(res))
					return nil
				},
			},

			{
				Name: "get-volume",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "addr",
						Required: true,
					},
					&cli.Uint32Flag{
						Name:     "pool-id",
						Required: true,
					},
					&cli.Uint32Flag{
						Name: "volume-id",
					},
					&cli.StringFlag{
						Name: "volume-name",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					c, closer, err := NewSigmaClient(ctx, cmd.String("addr"))
					if err != nil {
						return err
					}
					defer closer()

					req := &pb.GetVolumeRequest{
						PoolId: cmd.Uint32("pool-id"),
					}
					if cmd.Uint32("volume-id") != 0 {
						req.VolumeId = ptr.Of(cmd.Uint32("volume-id"))
					}
					if cmd.String("volume-name") != "" {
						req.VolumeName = ptr.Of(cmd.String("volume-name"))
					}
					res, err := c.GetVolume(ctx, req)
					if err != nil {
						return err
					}
					fmt.Println(JSON(res))
					return nil
				},
			},

			{
				Name: "create-volume",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "addr",
						Required: true,
					},
					&cli.Uint32Flag{
						Name:     "pool-id",
						Required: true,
					},
					&cli.Uint32Flag{
						Name: "volume-id",
					},
					&cli.StringFlag{
						Name: "volume-name",
					},
					&cli.Uint64Flag{
						Name:     "size",
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					c, closer, err := NewSigmaClient(ctx, cmd.String("addr"))
					if err != nil {
						return err
					}
					defer closer()

					req := &pb.CreateVolumeRequest{
						PoolId:     cmd.Uint32("pool-id"),
						VolumeSize: cmd.Uint64("size"),
					}
					if cmd.Uint32("volume-id") != 0 {
						req.VolumeId = ptr.Of(cmd.Uint32("volume-id"))
					}
					if cmd.String("volume-name") != "" {
						req.VolumeName = ptr.Of(cmd.String("volume-name"))
					}
					res, err := c.CreateVolume(ctx, req)
					if err != nil {
						return err
					}
					fmt.Println(JSON(res))
					return nil
				},
			},

			{
				Name: "delete-volume",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "addr",
						Required: true,
					},
					&cli.Uint32Flag{
						Name:     "pool-id",
						Required: true,
					},
					&cli.Uint32Flag{
						Name:     "volume-id",
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					c, closer, err := NewSigmaClient(ctx, cmd.String("addr"))
					if err != nil {
						return err
					}
					defer closer()

					res, err := c.DeleteVolume(ctx, &pb.DeleteVolumeRequest{
						PoolId:   cmd.Uint32("pool-id"),
						VolumeId: cmd.Uint32("volume-id"),
					})
					if err != nil {
						return err
					}
					fmt.Println(JSON(res))
					return nil
				},
			},

			{
				Name:   "play-up",
				Action: PlayUp,
			},

			{
				Name:   "play-down",
				Action: PlayDown,
			},
		},
	}

	err := cmd.Run(context.Background(), os.Args)
	if err != nil {
		telemetry.NewLogger().Error("error running command", telemetry.Error(err))
	}
}
