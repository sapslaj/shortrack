package k8sprovisioner

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	storagehelpers "k8s.io/component-helpers/storage/volume"
	"sigs.k8s.io/sig-storage-lib-external-provisioner/v6/controller"

	"github.com/sapslaj/shortrack/pb"
)

type K8sProvisioner struct {
	K8sClient kubernetes.Interface
}

type StorageClassParameters struct {
	PoolName     string `mapstructure:"poolName"`
	ServerAddr   string `mapstructure:"serverAddr"`
	TargetPortal string `mapstructure:"targetPortal"`
	FSType       string `mapstructure:"fsType"`
}

const (
	AnnotationIQN        = "shortrack.sapslaj.xyz/iqn"
	AnnotationLUN        = "shortrack.sapslaj.xyz/lun"
	AnnotationPoolID     = "shortrack.sapslaj.xyz/pool-id"
	AnnotationStatus     = "shortrack.sapslaj.xyz/status"
	AnnotationVolumeID   = "shortrack.sapslaj.xyz/volume-id"
	AnnotationVolumeName = "shortrack.sapslaj.xyz/volume-name"
	AnnotationVolumeSize = "shortrack.sapslaj.xyz/volume-size"
)

var _ controller.Provisioner = &K8sProvisioner{}

func ParseNumber[T any](raw string) (T, error) {
	var value T
	reflectValue := reflect.ValueOf(&value)
	elem := reflectValue.Elem()

	switch any(value).(type) {
	case int:
		valueInt, err := strconv.ParseInt(raw, 10, 0)
		if err != nil {
			return value, err
		}
		elem.SetInt(valueInt)
	case int8:
		valueInt, err := strconv.ParseInt(raw, 10, 8)
		if err != nil {
			return value, err
		}
		elem.SetInt(valueInt)
	case int16:
		valueInt, err := strconv.ParseInt(raw, 10, 16)
		if err != nil {
			return value, err
		}
		elem.SetInt(valueInt)
	case int32:
		valueInt, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			return value, err
		}
		elem.SetInt(valueInt)
	case int64:
		valueInt, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return value, err
		}
		elem.SetInt(valueInt)
	case uint8:
		valueUint, err := strconv.ParseUint(raw, 10, 8)
		if err != nil {
			return value, err
		}
		elem.SetUint(valueUint)
	case uint16:
		valueUint, err := strconv.ParseUint(raw, 10, 16)
		if err != nil {
			return value, err
		}
		elem.SetUint(valueUint)
	case uint32:
		valueUint, err := strconv.ParseUint(raw, 10, 32)
		if err != nil {
			return value, err
		}
		elem.SetUint(valueUint)
	case uint64:
		valueUint, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return value, err
		}
		elem.SetUint(valueUint)
	case uint:
		valueUint, err := strconv.ParseUint(raw, 10, 0)
		if err != nil {
			return value, err
		}
		elem.SetUint(valueUint)
	case float32:
		valueFloat, err := strconv.ParseFloat(raw, 32)
		if err != nil {
			return value, err
		}
		elem.SetFloat(valueFloat)
	case float64:
		valueFloat, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return value, err
		}
		elem.SetFloat(valueFloat)
	default:
		return value, fmt.Errorf("unsupported type: %T", value)
	}

	return value, nil
}

func (p *K8sProvisioner) GetStorageClassParameters(
	ctx context.Context,
	sc *storagev1.StorageClass,
) (StorageClassParameters, error) {
	var result StorageClassParameters
	err := mapstructure.Decode(sc.Parameters, &result)
	return result, err
}

func (p *K8sProvisioner) MakeSigmaClient(
	ctx context.Context,
	scp StorageClassParameters,
) (pb.SigmaClient, func() error, error) {
	if scp.ServerAddr == "" {
		return nil, nil, fmt.Errorf("serverAddr must be configured in the StorageClass")
	}
	conn, err := grpc.NewClient(scp.ServerAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}

	closer := func() error {
		return conn.Close()
	}
	return pb.NewSigmaClient(conn), closer, nil
}

func (p *K8sProvisioner) Provision(
	ctx context.Context,
	options controller.ProvisionOptions,
) (*corev1.PersistentVolume, controller.ProvisioningState, error) {
	if options.PVC.Spec.Selector != nil {
		return nil, controller.ProvisioningFinished, fmt.Errorf("claim Selector is not supported")
	}

	pvcNamespace := options.PVC.Namespace
	pvcName := options.PVC.Name

	pvName := strings.Join([]string{pvcNamespace, pvcName, options.PVName}, "-")

	scp, err := p.GetStorageClassParameters(ctx, options.StorageClass)
	if err != nil {
		return nil, controller.ProvisioningFinished, err
	}

	sigmaClient, sigmaClientCloser, err := p.MakeSigmaClient(ctx, scp)
	if err != nil {
		return nil, controller.ProvisioningFinished, err
	}
	defer sigmaClientCloser()

	var poolID uint32
	var scPoolIDAnnotation string
	pvcPoolIDAnnotation := options.PVC.Annotations[AnnotationPoolID]
	if pvcPoolIDAnnotation != "" {
		poolID, err = ParseNumber[uint32](pvcPoolIDAnnotation)
		if err != nil {
			return nil, controller.ProvisioningFinished, fmt.Errorf("error parsing PersistentVolumeClaim pool-id annotation: %w", err)
		}
	}
	if poolID == 0 {
		scPoolIDAnnotation = options.StorageClass.Annotations[AnnotationPoolID]
		if scPoolIDAnnotation != "" {
			poolID, err = ParseNumber[uint32](scPoolIDAnnotation)
			if err != nil {
				return nil, controller.ProvisioningFinished, fmt.Errorf("error parsing StorageClass pool-id annotation: %w", err)
			}
		}
	}
	if poolID == 0 {
		if scp.PoolName == "" {
			return nil, controller.ProvisioningFinished, fmt.Errorf("poolName must be configured in the StorageClass")
		}
		res, err := sigmaClient.ListPools(ctx, &pb.ListPoolsRequest{})
		if err != nil {
			return nil, controller.ProvisioningFinished, err
		}

		for _, pool := range res.Pools {
			if pool.PoolName != scp.PoolName {
				continue
			}
			poolID = pool.PoolId
			break
		}
	}
	if poolID == 0 {
		res, err := sigmaClient.CreatePool(ctx, &pb.CreatePoolRequest{
			PoolName: &scp.PoolName,
		})
		if err != nil {
			return nil, controller.ProvisioningFinished, err
		}
		poolID = res.PoolId
	}

	if scPoolIDAnnotation == "" {
		if options.StorageClass.Annotations == nil {
			options.StorageClass.Annotations = map[string]string{}
		}
		options.StorageClass.Annotations[AnnotationPoolID] = fmt.Sprint(poolID)
		_, err := p.K8sClient.StorageV1().StorageClasses().Update(ctx, options.StorageClass, metav1.UpdateOptions{})
		if err != nil {
			return nil, "", fmt.Errorf("error updating storage class annotations: %w", err)
		}
	}

	volumeInfo, err := sigmaClient.CreateVolume(ctx, &pb.CreateVolumeRequest{
		PoolId:     poolID,
		VolumeSize: uint64(options.PVC.Spec.Resources.Requests.Storage().Value()),
		VolumeName: &pvName,
	})
	if err != nil {
		s := status.Convert(err)
		if s.Code() == codes.AlreadyExists {
			volumeInfo, err = sigmaClient.GetVolume(ctx, &pb.GetVolumeRequest{
				PoolId:     poolID,
				VolumeName: &pvName,
			})
		} else {
			return nil, "", err
		}
	}

	fsType := "ext4"
	if scp.FSType != "" {
		fsType = scp.FSType
	}

	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: options.PVName,
			Annotations: map[string]string{
				AnnotationIQN:        volumeInfo.Iqn,
				AnnotationLUN:        fmt.Sprint(volumeInfo.Lun),
				AnnotationPoolID:     fmt.Sprint(volumeInfo.PoolId),
				AnnotationStatus:     volumeInfo.Status.Enum().String(),
				AnnotationVolumeID:   fmt.Sprint(volumeInfo.VolumeId),
				AnnotationVolumeName: volumeInfo.VolumeName,
				AnnotationVolumeSize: fmt.Sprint(volumeInfo.VolumeSize),
			},
		},
		Spec: corev1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: *options.StorageClass.ReclaimPolicy,
			AccessModes:                   options.PVC.Spec.AccessModes,
			MountOptions:                  options.StorageClass.MountOptions,
			Capacity: corev1.ResourceList{
				corev1.ResourceName(corev1.ResourceStorage): options.PVC.Spec.Resources.Requests[corev1.ResourceName(corev1.ResourceStorage)],
			},
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				ISCSI: &corev1.ISCSIPersistentVolumeSource{
					TargetPortal: scp.TargetPortal,
					IQN:          volumeInfo.Iqn,
					Lun:          int32(volumeInfo.Lun),
					FSType:       fsType,
				},
			},
		},
	}
	return pv, controller.ProvisioningFinished, nil
}

func (p *K8sProvisioner) Delete(ctx context.Context, pv *corev1.PersistentVolume) error {
	storageClassName := storagehelpers.GetPersistentVolumeClass(pv)
	if storageClassName == "" {
		return fmt.Errorf("volume has no storage class")
	}
	storageClass, err := p.K8sClient.StorageV1().StorageClasses().Get(ctx, storageClassName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	scp, err := p.GetStorageClassParameters(ctx, storageClass)
	if err != nil {
		return err
	}

	sigmaClient, sigmaClientCloser, err := p.MakeSigmaClient(ctx, scp)
	if err != nil {
		return err
	}
	defer sigmaClientCloser()

	var poolID uint32
	pvPoolIDAnnotation := pv.Annotations[AnnotationPoolID]
	if pvPoolIDAnnotation != "" {
		poolID, err = ParseNumber[uint32](pvPoolIDAnnotation)
		if err != nil {
			return fmt.Errorf("error parsing PersistentVolume pool-id annotation: %w", err)
		}
	}
	if poolID == 0 {
		scPoolIDAnnotation := storageClass.Annotations[AnnotationPoolID]
		if scPoolIDAnnotation != "" {
			poolID, err = ParseNumber[uint32](scPoolIDAnnotation)
			if err != nil {
				return fmt.Errorf("error parsing StorageClass pool-id annotation: %w", err)
			}
		}
	}
	if poolID == 0 {
		if scp.PoolName == "" {
			return fmt.Errorf("poolName must be configured in the StorageClass")
		}
		res, err := sigmaClient.ListPools(ctx, &pb.ListPoolsRequest{})
		if err != nil {
			return err
		}

		for _, pool := range res.Pools {
			if pool.PoolName != scp.PoolName {
				continue
			}
			poolID = pool.PoolId
			break
		}
	}
	if poolID == 0 {
		// TODO: logging around unknown pool ID
		return nil
	}

	var volumeID uint32

	volumeIDAnnotation := pv.Annotations[AnnotationVolumeID]
	if volumeIDAnnotation != "" {
		volumeID, err = ParseNumber[uint32](volumeIDAnnotation)
		if err != nil {
			return fmt.Errorf("error parsing PersistentVolume volume-id annotation: %w", err)
		}
	}
	if volumeID == 0 {
		info, err := sigmaClient.GetVolume(ctx, &pb.GetVolumeRequest{
			PoolId:     poolID,
			VolumeName: &pv.ObjectMeta.Name,
		})
		if err != nil {
			return err
		}
		volumeID = info.VolumeId
	}

	_, err = sigmaClient.DeleteVolume(ctx, &pb.DeleteVolumeRequest{
		PoolId:   poolID,
		VolumeId: volumeID,
	})
	return err
}
