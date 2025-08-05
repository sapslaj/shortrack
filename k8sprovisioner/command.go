package k8sprovisioner

import (
	"context"
	"os"

	"github.com/urfave/cli/v3"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/sig-storage-lib-external-provisioner/v6/controller"

	"github.com/sapslaj/shortrack/pkg/env"
)

func Command() *cli.Command {
	return &cli.Command{
		Name: "k8s-provisioner",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			kubeconfig := os.Getenv("KUBECONFIG")
			var config *rest.Config
			if kubeconfig != "" {
				var err error
				config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
				if err != nil {
					return err
				}
			} else {
				var err error
				config, err = rest.InClusterConfig()
				if err != nil {
					return err
				}
			}

			clientset, err := kubernetes.NewForConfig(config)
			if err != nil {
				return err
			}

			serverVersion, err := clientset.Discovery().ServerVersion()
			if err != nil {
				return err
			}

			leaderElection, err := env.GetDefault("ENABLE_LEADER_ELECTION", false)
			if err != nil {
				return err
			}

			p := &K8sProvisioner{
				K8sClient: clientset,
			}

			pc := controller.NewProvisionController(
				clientset,
				"shortrack.sapslaj.xyz",
				p,
				serverVersion.GitVersion,
				controller.LeaderElection(leaderElection),
			)
			pc.Run(ctx)
			return nil
		},
	}
}
