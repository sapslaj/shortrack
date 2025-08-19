package sigma

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sapslaj/shortrack/pb"
)

type ServerConfig struct {
	BaseIQN    string `json:"base_iqn"`
	VolumesDir string `json:"volumes_dir"`
	Portal     string `json:"portal"`
}

type StateVolume struct {
	ID     uint16 `json:"id"`
	Name   string `json:"name"`
	PoolID uint16 `json:"pool_id"`
	Size   int64  `json:"size"`
}

type StatePool struct {
	ID      uint16                 `json:"id"`
	Name    string                 `json:"name"`
	Volumes map[uint16]StateVolume `json:"volumes"`
}

type State struct {
	MaxLUNID uint16               `json:"max_lun_id"`
	Pools    map[uint16]StatePool `json:"pools"`
}

type Server struct {
	pb.UnimplementedSigmaServer
	Config     ServerConfig
	StateMutex sync.RWMutex
	State      State
}

var _ pb.SigmaServer = (*Server)(nil)

func (s *Server) LoadState(ctx context.Context) error {
	data, err := os.ReadFile(path.Join(s.Config.VolumesDir, fmt.Sprintf("shortrack-state.%s.json", s.Config.BaseIQN)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("unable to read state file: %w", err)
	}
	err = json.Unmarshal(data, &s.State)
	if err != nil {
		return fmt.Errorf("unable to parse state data: %w", err)
	}
	return nil
}

func (s *Server) SaveState(ctx context.Context) error {
	f, err := os.OpenFile(
		path.Join(s.Config.VolumesDir, fmt.Sprintf("shortrack-state.%s.json", s.Config.BaseIQN)),
		os.O_WRONLY|os.O_CREATE|os.O_TRUNC,
		0o644,
	)
	if err != nil {
		return fmt.Errorf("unable to save state file: %w", err)
	}
	defer f.Close()
	data, err := json.MarshalIndent(s.State, "", "  ")
	if err != nil {
		return fmt.Errorf("unable to marshal state: %w", err)
	}
	data = append(data, []byte("\n")...)
	_, err = f.Write(data)
	if err != nil {
		return fmt.Errorf("unable to write state file: %w", err)
	}
	return nil
}

func (s *Server) ReconcileAll(ctx context.Context) error {
	s.StateMutex.RLock()
	defer s.StateMutex.RUnlock()

	err := SetupLIO()
	if err != nil {
		return err
	}

	existingTargets, err := ListTargets(ctx, s.Config.BaseIQN)
	if err != nil {
		return err
	}
	for _, existingTarget := range existingTargets {
		_, shouldExist := s.State.Pools[existingTarget.ID]
		if !shouldExist {
			err = DeleteTarget(ctx, existingTarget)
			if err != nil {
				return err
			}
		}
	}

	for _, pool := range s.State.Pools {
		target := Target{
			ID:   pool.ID,
			Name: pool.Name,
			IQN:  fmt.Sprintf("%s:%s", s.Config.BaseIQN, pool.Name),
		}
		err = UpsertTarget(ctx, target)
		if err != nil {
			return err
		}

		existingPortals, err := ListPortals(ctx, target)
		if err != nil {
			return err
		}

		for _, existingPortal := range existingPortals {
			if existingPortal == s.Config.Portal {
				continue
			}

			err = DeletePortal(ctx, target, existingPortal)
			if err != nil {
				return err
			}
		}

		err = UpsertPortal(ctx, target, s.Config.Portal)
		if err != nil {
			return err
		}

		existingLUNs, err := ListLUNs(ctx, target)
		if err != nil {
			return err
		}

		for _, existingLUN := range existingLUNs {
			_, shouldExist := pool.Volumes[existingLUN]
			if !shouldExist {
				err = DeleteLUN(ctx, target, existingLUN)
				if err != nil {
					return err
				}
			}
		}
	}

	backstores := []Backstore{}

	for _, pool := range s.State.Pools {
		for _, volume := range pool.Volumes {
			target := Target{
				ID:   pool.ID,
				Name: pool.Name,
				IQN:  fmt.Sprintf("%s:%s", s.Config.BaseIQN, pool.Name),
			}
			backstore := Backstore{
				Type:     BackstoreFileio,
				ID:       volume.ID,
				Name:     volume.Name,
				FilePath: path.Join(s.Config.VolumesDir, fmt.Sprintf("%s-%s.img", pool.Name, volume.Name)),
			}
			size, err := TouchDiskFile(backstore.FilePath, max(1024*1024*1024, volume.Size))
			if err != nil {
				return err
			}
			backstore.FileSize = uint64(size)

			backstores = append(backstores, backstore)

			err = UpsertBackstore(ctx, backstore)
			if err != nil {
				return err
			}

			err = UpsertLUN(ctx, target, volume.ID, backstore)
			if err != nil {
				return err
			}
		}
	}

	existingBackstores, err := ListBackstores(ctx)
	if err != nil {
		return err
	}

	for _, existingBackstore := range existingBackstores {
		if slices.ContainsFunc(backstores, func(backstore Backstore) bool {
			if backstore.ID != existingBackstore.ID {
				return false
			}
			if backstore.Type != existingBackstore.Type {
				return false
			}
			if backstore.Name != existingBackstore.Name {
				return false
			}
			return true
		}) {
			continue
		}

		err = DeleteBackstore(ctx, existingBackstore)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *Server) ListPools(ctx context.Context, in *pb.ListPoolsRequest) (*pb.ListPoolsResponse, error) {
	res := &pb.ListPoolsResponse{
		Pools: []*pb.PoolInfo{},
	}

	s.StateMutex.RLock()
	defer s.StateMutex.RUnlock()

	for _, pool := range s.State.Pools {
		res.Pools = append(res.Pools, &pb.PoolInfo{
			PoolId:   uint32(pool.ID),
			PoolName: pool.Name,
			Status:   pb.ResourceStatus_RESOURCE_ACTIVE,
			Iqn:      fmt.Sprintf("%s:%s", s.Config.BaseIQN, pool.Name),
		})
	}

	return res, nil
}

func (s *Server) GetPool(ctx context.Context, in *pb.GetPoolRequest) (*pb.PoolInfo, error) {
	res := &pb.PoolInfo{
		Status: pb.ResourceStatus_RESOURCE_INVALID_STATUS,
	}
	if in.PoolId == nil && in.PoolName == nil {
		return res, status.Errorf(codes.InvalidArgument, "pool_id or pool_name must be specified")
	}

	s.StateMutex.RLock()
	defer s.StateMutex.RUnlock()

	var found *StatePool
	if in.PoolId != nil {
		res.PoolId = *in.PoolId
		pool, exists := s.State.Pools[uint16(*in.PoolId)]
		if !exists {
			res.Status = pb.ResourceStatus_RESOURCE_ABSENT
			return res, status.Errorf(codes.NotFound, "pool_id %d not found", *in.PoolId)
		}
		found = &pool
	} else {
		res.PoolName = *in.PoolName
		for _, pool := range s.State.Pools {
			if *in.PoolName == pool.Name {
				found = &pool
				break
			}
		}

		if found == nil {
			res.Status = pb.ResourceStatus_RESOURCE_ABSENT
			return res, status.Errorf(codes.NotFound, "pool_name %s not found", *in.PoolName)
		}
	}

	// TODO: check pool status
	res.PoolId = uint32(found.ID)
	res.PoolName = found.Name
	res.Status = pb.ResourceStatus_RESOURCE_ACTIVE
	res.Iqn = fmt.Sprintf("%s:%s", s.Config.BaseIQN, found.Name)

	return res, nil
}

func (s *Server) CreatePool(ctx context.Context, in *pb.CreatePoolRequest) (*pb.PoolInfo, error) {
	res := &pb.PoolInfo{
		Status: pb.ResourceStatus_RESOURCE_INVALID_STATUS,
	}
	pool := StatePool{}

	if in.PoolId != nil {
		pool.ID = uint16(*in.PoolId)
		res.PoolId = *in.PoolId

		if pool.ID == 0 {
			return res, status.Errorf(
				codes.InvalidArgument,
				"pool 0 is reserved, use a different id",
			)
		}
	} else {
		targets, err := ListTargets(ctx, s.Config.BaseIQN)
		if err != nil {
			return res, status.Errorf(
				codes.FailedPrecondition,
				"could not generate a new pool ID: %v",
				err,
			)
		}

		for i := range uint16(65535) {
			if i == 0 {
				continue
			}
			if slices.ContainsFunc(targets, func(target Target) bool {
				return target.ID == i
			}) {
				continue
			}
			pool.ID = i
			break
		}
	}

	if pool.ID == 0 {
		return res, status.Errorf(
			codes.ResourceExhausted,
			"could not generate a new pool ID",
		)
	}

	s.StateMutex.Lock()
	defer s.StateMutex.Unlock()

	var conflict *StatePool
	for _, existingPool := range s.State.Pools {
		if existingPool.ID == pool.ID {
			conflict = &existingPool
			break
		}
		if in.PoolName != nil && existingPool.Name == pool.Name {
			conflict = &existingPool
			break
		}
	}
	if conflict != nil {
		return res, status.Errorf(
			codes.AlreadyExists,
			"pool with id %d and name %q already exists",
			conflict.ID,
			conflict.Name,
		)
	}

	pool.Name = fmt.Sprintf("%d", pool.ID)
	if in.PoolName != nil {
		pool.Name = *in.PoolName
	}

	res.PoolId = uint32(pool.ID)
	res.PoolName = pool.Name
	res.Status = pb.ResourceStatus_RESOURCE_DEGRADED
	res.Iqn = fmt.Sprintf("%s:%s", s.Config.BaseIQN, pool.Name)

	target := Target{
		ID:   pool.ID,
		Name: pool.Name,
		IQN:  fmt.Sprintf("%s:%s", s.Config.BaseIQN, pool.Name),
	}
	err := UpsertTarget(ctx, target)
	if err != nil {
		return res, status.Errorf(
			codes.Aborted,
			"could not create target group pool: %v",
			err,
		)
	}

	res.Status = pb.ResourceStatus_RESOURCE_ACTIVE

	if s.State.Pools == nil {
		s.State.Pools = map[uint16]StatePool{}
	}
	s.State.Pools[pool.ID] = pool

	err = s.SaveState(ctx)
	if err != nil {
		return res, status.Errorf(
			codes.Internal,
			"could not save state: %v",
			err,
		)
	}

	return res, nil
}

func (s *Server) DeletePool(ctx context.Context, in *pb.DeletePoolRequest) (*pb.PoolInfo, error) {
	res := &pb.PoolInfo{
		PoolId: in.PoolId,
		Status: pb.ResourceStatus_RESOURCE_INVALID_STATUS,
	}

	targets, err := ListTargets(ctx, s.Config.BaseIQN)
	if err != nil {
		return res, status.Errorf(
			codes.FailedPrecondition,
			"could not list active pools: %v",
			err,
		)
	}

	res.Status = pb.ResourceStatus_RESOURCE_ABSENT
	var target Target
	for _, t := range targets {
		if t.ID != uint16(in.PoolId) {
			continue
		}
		res.Status = pb.ResourceStatus_RESOURCE_ACTIVE
		target = t
		break
	}

	s.StateMutex.Lock()
	defer s.StateMutex.Unlock()

	err = DeleteTarget(ctx, target)
	if err != nil {
		return res, status.Errorf(
			codes.Aborted,
			"could not delete target group pool: %v",
			err,
		)
	}

	delete(s.State.Pools, uint16(in.PoolId))

	err = s.SaveState(ctx)
	if err != nil {
		return res, status.Errorf(
			codes.Internal,
			"could not save state: %v",
			err,
		)
	}

	return res, nil
}

func (s *Server) ListVolumes(ctx context.Context, in *pb.ListVolumesRequest) (*pb.ListVolumesResponse, error) {
	res := &pb.ListVolumesResponse{
		Volumes: []*pb.VolumeInfo{},
	}

	s.StateMutex.RLock()
	defer s.StateMutex.RUnlock()

	poolID := uint16(in.PoolId)
	pool, exists := s.State.Pools[poolID]
	if !exists {
		return res, status.Errorf(
			codes.InvalidArgument,
			"pool id %d does not exist",
			poolID,
		)
	}

	for _, volume := range pool.Volumes {
		// TODO: get volume state
		res.Volumes = append(res.Volumes, &pb.VolumeInfo{
			PoolId:     in.PoolId,
			VolumeId:   uint32(volume.ID),
			VolumeName: volume.Name,
			VolumeSize: uint64(volume.Size),
			Status:     pb.ResourceStatus_RESOURCE_ACTIVE,
			Iqn:        fmt.Sprintf("%s:%s", s.Config.BaseIQN, pool.Name),
			Lun:        int64(volume.ID),
		})
	}

	return res, nil
}

func (s *Server) GetVolume(ctx context.Context, in *pb.GetVolumeRequest) (*pb.VolumeInfo, error) {
	res := &pb.VolumeInfo{
		PoolId: in.PoolId,
		Status: pb.ResourceStatus_RESOURCE_INVALID_STATUS,
	}
	if in.VolumeId == nil && in.VolumeName == nil {
		return res, status.Errorf(codes.InvalidArgument, "volume_id or volume_name must be specified")
	}

	s.StateMutex.RLock()
	defer s.StateMutex.RUnlock()

	poolID := uint16(in.PoolId)
	pool, exists := s.State.Pools[poolID]
	if !exists {
		return res, status.Errorf(
			codes.InvalidArgument,
			"pool id %d does not exist",
			poolID,
		)
	}

	var found *StateVolume
	if in.VolumeId != nil {
		res.VolumeId = *in.VolumeId
		volume, exists := pool.Volumes[uint16(*in.VolumeId)]
		if !exists {
			res.Status = pb.ResourceStatus_RESOURCE_ABSENT
			return res, status.Errorf(
				codes.NotFound,
				"volume_id %d not found",
				*in.VolumeId,
			)
		}
		found = &volume
	} else {
		res.VolumeName = *in.VolumeName
		for _, volume := range pool.Volumes {
			if *in.VolumeName == pool.Name {
				found = &volume
				break
			}
		}

		if found == nil {
			res.Status = pb.ResourceStatus_RESOURCE_ABSENT
			return res, status.Errorf(codes.NotFound, "volume_name %s not found", *in.VolumeName)
		}
	}

	// TODO: check volume status
	res.PoolId = uint32(found.PoolID)
	res.VolumeId = uint32(found.ID)
	res.VolumeName = found.Name
	res.Status = pb.ResourceStatus_RESOURCE_ACTIVE
	res.VolumeSize = uint64(found.Size)
	res.Lun = int64(found.ID)
	res.Iqn = fmt.Sprintf("%s:%s", s.Config.BaseIQN, pool.Name)

	return res, nil
}

func (s *Server) CreateVolume(ctx context.Context, in *pb.CreateVolumeRequest) (*pb.VolumeInfo, error) {
	res := &pb.VolumeInfo{
		PoolId: in.PoolId,
		Status: pb.ResourceStatus_RESOURCE_INVALID_STATUS,
	}
	volume := StateVolume{}

	if in.VolumeSize < 1 {
		return res, status.Error(
			codes.InvalidArgument,
			"volume needs to be more than 0 bytes in size",
		)
	}

	volume.Size = int64(in.VolumeSize)

	s.StateMutex.Lock()
	defer s.StateMutex.Unlock()

	if in.VolumeId != nil {
		volume.ID = uint16(*in.VolumeId)

		if volume.ID == 0 {
			return res, status.Error(
				codes.InvalidArgument,
				"volume 0 is reserved, use a different id",
			)
		}
	} else {
		backstores, err := ListBackstores(ctx)
		if err != nil {
			return res, status.Errorf(
				codes.FailedPrecondition,
				"could not generate a new volume ID: %v",
				err,
			)
		}

		ids := []uint16{}
		for _, backstore := range backstores {
			ids = append(ids, backstore.ID)
		}
		if len(ids) == 0 {
			volume.ID = 1
		} else {
			slices.Sort(ids)
			// FIXME: uint16 overflow is undefined
			volume.ID = max(s.State.MaxLUNID, ids[len(ids)-1]) + 1
			// TODO: check to make sure the calculated volume ID doesn't conflict
		}
	}

	if volume.ID == 0 {
		return res, status.Errorf(
			codes.ResourceExhausted,
			"could not generate a new volume ID",
		)
	}

	s.State.MaxLUNID = max(s.State.MaxLUNID, volume.ID)

	poolID := uint16(in.PoolId)
	pool, exists := s.State.Pools[poolID]
	if !exists {
		return res, status.Errorf(
			codes.InvalidArgument,
			"pool id %d does not exist",
			poolID,
		)
	}

	var conflict *StateVolume
	for _, existingVolume := range pool.Volumes {
		if existingVolume.ID == volume.ID {
			conflict = &existingVolume
			break
		}
		if existingVolume.Name == volume.Name {
			conflict = &existingVolume
			break
		}
	}
	if conflict != nil {
		return res, status.Errorf(
			codes.AlreadyExists,
			"volume with id %d and name %q already exists in pool %d",
			conflict.ID,
			conflict.Name,
			poolID,
		)
	}

	volume.Name = fmt.Sprintf("%d", volume.ID)
	if in.VolumeName != nil {
		volume.Name = *in.VolumeName
	}

	res.VolumeId = uint32(volume.ID)
	res.Status = pb.ResourceStatus_RESOURCE_DEGRADED
	res.VolumeName = volume.Name
	res.VolumeSize = uint64(volume.Size)
	res.Lun = int64(volume.ID)
	res.Iqn = fmt.Sprintf("%s:%s", s.Config.BaseIQN, pool.Name)

	backstore := Backstore{
		Type:     BackstoreFileio,
		ID:       volume.ID,
		Name:     volume.Name,
		FilePath: path.Join(s.Config.VolumesDir, fmt.Sprintf("%s-%s.img", pool.Name, volume.Name)),
	}

	_, err := TouchDiskFile(backstore.FilePath, volume.Size)
	if err != nil {
		return res, status.Errorf(
			codes.Aborted,
			"could not create disk file: %v",
			err,
		)
	}

	err = UpsertBackstore(ctx, backstore)
	if err != nil {
		return res, status.Errorf(
			codes.Aborted,
			"could not create backstore: %v",
			err,
		)
	}

	target := Target{
		ID:   pool.ID,
		Name: pool.Name,
		IQN:  fmt.Sprintf("%s:%s", s.Config.BaseIQN, pool.Name),
	}

	err = UpsertLUN(ctx, target, volume.ID, backstore)
	if err != nil {
		return res, status.Errorf(
			codes.Aborted,
			"could not create LUN: %v",
			err,
		)
	}

	res.Status = pb.ResourceStatus_RESOURCE_ACTIVE

	if s.State.Pools == nil {
		s.State.Pools = map[uint16]StatePool{}
	}
	if s.State.Pools[poolID].Volumes == nil {
		pool := s.State.Pools[poolID]
		pool.Volumes = map[uint16]StateVolume{}
		s.State.Pools[poolID] = pool
	}
	s.State.Pools[poolID].Volumes[volume.ID] = volume

	err = s.SaveState(ctx)
	if err != nil {
		return res, status.Errorf(
			codes.Internal,
			"could not save state: %v",
			err,
		)
	}

	return res, nil
}

func (s *Server) DeleteVolume(ctx context.Context, in *pb.DeleteVolumeRequest) (*pb.VolumeInfo, error) {
	res := &pb.VolumeInfo{
		PoolId:   in.PoolId,
		VolumeId: in.VolumeId,
		Status:   pb.ResourceStatus_RESOURCE_ACTIVE,
		Lun:      int64(in.VolumeId),
	}

	targets, err := ListTargets(ctx, s.Config.BaseIQN)
	if err != nil {
		return res, status.Errorf(
			codes.FailedPrecondition,
			"could not list targets: %v",
			err,
		)
	}

	var target *Target
	for _, t := range targets {
		if in.PoolId != uint32(t.ID) {
			continue
		}
		target = &t
	}
	if target == nil {
		return res, status.Errorf(
			codes.InvalidArgument,
			"pool id %d does not exist",
			in.PoolId,
		)
	}

	luns, err := ListLUNs(ctx, *target)
	if err != nil {
		return res, status.Errorf(
			codes.FailedPrecondition,
			"could not list active LUNs: %v",
			err,
		)
	}

	if !slices.Contains(luns, uint16(in.VolumeId)) {
		res.Status = pb.ResourceStatus_RESOURCE_DEGRADED
	}

	backstores, err := ListBackstores(ctx)
	if err != nil {
		return res, status.Errorf(
			codes.FailedPrecondition,
			"could not list backstores: %v",
			err,
		)
	}

	backstore := Backstore{
		ID: uint16(in.VolumeId),
	}
	for _, existingBackstore := range backstores {
		if existingBackstore.ID == backstore.ID {
			backstore = existingBackstore
			break
		}
	}
	if backstore.Type == "" {
		res.Status = pb.ResourceStatus_RESOURCE_DEGRADED
		backstore.Type = BackstoreFileio
	}

	s.StateMutex.Lock()
	defer s.StateMutex.Unlock()

	pool, poolExists := s.State.Pools[uint16(in.PoolId)]
	if poolExists {
		existingVolume, volumeExists := pool.Volumes[uint16(in.VolumeId)]
		if volumeExists {
			backstore.Name = existingVolume.Name
			backstore.FilePath = path.Join(s.Config.VolumesDir, fmt.Sprintf("%s-%s.img", pool.Name, existingVolume.Name))
			backstore.FileSize = uint64(existingVolume.Size)
		}
		res.Iqn = fmt.Sprintf("%s:%s", s.Config.BaseIQN, pool.Name)
	}

	err = DeleteLUN(ctx, *target, uint16(in.VolumeId))
	if err != nil {
		return res, status.Errorf(
			codes.Aborted,
			"could not delete LUN: %v",
			err,
		)
	}

	err = DeleteBackstore(ctx, backstore)
	if err != nil {
		return res, status.Errorf(
			codes.Aborted,
			"could not delete backstore: %v",
			err,
		)
	}

	if backstore.FilePath != "" {
		err = DeleteDiskFile(backstore.FilePath)
		if err != nil {
			return res, status.Errorf(
				codes.Aborted,
				"could not delete disk file: %v",
				err,
			)
		}
	}

	if poolExists {
		delete(s.State.Pools[pool.ID].Volumes, uint16(in.VolumeId))
	}

	err = s.SaveState(ctx)
	if err != nil {
		return res, status.Errorf(
			codes.Internal,
			"could not save state: %v",
			err,
		)
	}

	return res, nil
}

type BackstoreType string

const (
	BackstoreBlock   BackstoreType = "block"
	BackstoreFileio  BackstoreType = "fileio"
	BackstorePscsi   BackstoreType = "pscsi"
	BackstoreRamdisk BackstoreType = "ramdisk"
)

type Backstore struct {
	Type BackstoreType
	ID   uint16
	Name string

	FilePath string
	FileSize uint64
}

type Target struct {
	ID   uint16
	Name string
	IQN  string
}

func ReadConfig(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func ReadConfigString(path string) (string, error) {
	s, err := ReadConfig(path)
	return string(s), err
}

func WriteConfig(path string, data []byte) error {
	stat, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	err = os.WriteFile(path, data, stat.Mode())
	if err != nil {
		return err
	}
	return nil
}

func WriteConfigString(path string, data string) error {
	return WriteConfig(path, []byte(data))
}

func SetConfig(path string, data string) error {
	value, err := ReadConfigString(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if strings.TrimSpace(value) != strings.TrimSpace(data) {
		err = WriteConfigString(path, data)
		if err != nil {
			return err
		}
	}
	return nil
}

func TouchDiskFile(path string, size int64) (newSize int64, err error) {
	newSize = 0
	exists := true
	existingSize := int64(0)

	stat, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		if os.IsNotExist(err) {
			exists = false
		}
		newSize = 0
	}

	if exists && stat != nil {
		existingSize = stat.Size()
	}
	if existingSize >= size {
		newSize = existingSize
		return
	}

	fd, err := os.Create(path)
	defer func() {
		if fd != nil {
			err = errors.Join(err, fd.Close())
		}
	}()
	if err != nil {
		return
	}

	err = syscall.Fallocate(int(fd.Fd()), 0, existingSize, size-existingSize)
	if err != nil {
		return
	}
	newSize = size

	return
}

func DeleteDiskFile(path string) error {
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func SetupLIO() error {
	// TODO: wait for configfs to mount
	configFSDir := "/sys/kernel/config"
	err := os.MkdirAll(path.Join(configFSDir, "target"), 0o755)
	if err != nil {
		return err
	}
	err = os.MkdirAll(path.Join(configFSDir, "target", "core"), 0o755)
	if err != nil {
		return err
	}
	err = os.MkdirAll(path.Join(configFSDir, "target", "iscsi"), 0o755)
	if err != nil {
		return err
	}
	return nil
}

func ListBackstores(ctx context.Context) ([]Backstore, error) {
	backstores := []Backstore{}

	backstoresDir := "/sys/kernel/config/target/core"
	backstoreEntries, err := os.ReadDir(backstoresDir)
	if err != nil && !os.IsNotExist(err) {
		return backstores, err
	}

	for _, backstoreEntry := range backstoreEntries {
		if !backstoreEntry.IsDir() {
			continue
		}
		if backstoreEntry.Name() == "alua" {
			continue
		}
		backstoreDir := path.Join(backstoresDir, backstoreEntry.Name())
		parts := strings.Split(backstoreEntry.Name(), "_")
		if len(parts) != 2 {
			continue
		}
		backstoreType := BackstoreType(parts[0])
		u, err := strconv.ParseUint(parts[1], 10, 16)
		if err != nil {
			return backstores, err
		}
		backstoreID := uint16(u)

		dataEntries, err := os.ReadDir(backstoreDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return backstores, err
		}

		if len(dataEntries) == 0 {
			continue
		}

		backstoreName := ""
		for _, dataEntry := range dataEntries {
			if !dataEntry.IsDir() {
				continue
			}
			backstoreName = dataEntry.Name()
		}
		if backstoreName == "" {
			continue
		}

		backstores = append(backstores, Backstore{
			Type: backstoreType,
			ID:   backstoreID,
			Name: backstoreName,
		})
	}

	return backstores, nil
}

func DeleteBackstore(ctx context.Context, backstore Backstore) error {
	backstoresDir := "/sys/kernel/config/target/core"
	backstoreDir := path.Join(backstoresDir, fmt.Sprintf("%s_%d", backstore.Type, backstore.ID))

	dataEntries, err := os.ReadDir(path.Join(backstoreDir))
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	for _, dataEntry := range dataEntries {
		if !dataEntry.IsDir() {
			continue
		}
		err = os.Remove(path.Join(backstoreDir, dataEntry.Name()))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
	}

	err = os.Remove(backstoreDir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

func UpsertBackstore(ctx context.Context, backstore Backstore) error {
	coreDir := "/sys/kernel/config/target/core"
	backstoreDir := path.Join(coreDir, fmt.Sprintf("%s_%d", backstore.Type, backstore.ID))
	backstoreDataDir := path.Join(backstoreDir, backstore.Name)

	switch backstore.Type {
	case BackstoreBlock:
		return fmt.Errorf("block backstore is not supported yet")

	case BackstoreFileio:
		err := os.MkdirAll(backstoreDataDir, 0o755)
		if err != nil {
			return err
		}

		err = WriteConfig(
			path.Join(backstoreDataDir, "control"),
			fmt.Appendf(nil, "fd_dev_name=%s", backstore.FilePath),
		)
		if err != nil {
			return err
		}
		err = WriteConfig(
			path.Join(backstoreDataDir, "control"),
			fmt.Appendf(nil, "fd_dev_size=%d", backstore.FileSize),
		)
		if err != nil {
			return err
		}
		err = WriteConfig(
			path.Join(backstoreDataDir, "attrib", "emulate_write_cache"),
			[]byte("1"),
		)
		if err != nil {
			return err
		}
		vpdUnitSerial, err := ReadConfigString(path.Join(backstoreDataDir, "wwn", "vpd_unit_serial"))
		if err != nil {
			return err
		}
		if strings.TrimSpace(strings.TrimPrefix(vpdUnitSerial, "T10 VPD Unit Serial Number:")) == "" {
			err = WriteConfig(
				path.Join(backstoreDataDir, "wwn", "vpd_unit_serial"),
				[]byte(backstore.Name),
			)
			if err != nil {
				return err
			}
		}
		enable, err := ReadConfigString(path.Join(backstoreDataDir, "enable"))
		if err != nil {
			if os.IsNotExist(err) {
				enable = "0"
			} else {
				return err
			}
		}
		if strings.TrimSpace(enable) == "0" {
			err = WriteConfig(
				path.Join(backstoreDataDir, "enable"),
				[]byte("1"),
			)
			if err != nil {
				return err
			}
		}

	case BackstorePscsi:
		return fmt.Errorf("pscsi backstore is not supported yet")

	case BackstoreRamdisk:
		return fmt.Errorf("ramdisk backstore is not supported yet")

	default:
		return fmt.Errorf("invalid backstore type: %v", backstore.Type)
	}

	return nil
}

func ListTargets(ctx context.Context, baseIQN string) ([]Target, error) {
	targets := []Target{}

	iscsiDir := "/sys/kernel/config/target/iscsi"
	iqnEntries, err := os.ReadDir(iscsiDir)
	if err != nil && !os.IsNotExist(err) {
		return targets, err
	}

	for _, iqnEntry := range iqnEntries {
		if !iqnEntry.IsDir() {
			continue
		}
		if !strings.HasPrefix(iqnEntry.Name(), baseIQN+":") {
			continue
		}

		iqn := iqnEntry.Name()
		iqnDir := path.Join(iscsiDir, iqn)
		name := strings.TrimPrefix(iqn, baseIQN+":")

		iqnTpgEntries, err := os.ReadDir(iqnDir)
		if err != nil && !os.IsNotExist(err) {
			return targets, err
		}

		for _, iqnTpgEntry := range iqnTpgEntries {
			if !iqnTpgEntry.IsDir() {
				continue
			}
			if !strings.HasPrefix(iqnTpgEntry.Name(), "tpgt_") {
				continue
			}
			u, err := strconv.ParseUint(strings.TrimPrefix(iqnTpgEntry.Name(), "tpgt_"), 10, 16)
			if err != nil {
				return targets, err
			}
			targets = append(targets, Target{
				ID:   uint16(u),
				Name: name,
				IQN:  iqn,
			})
		}
	}

	return targets, nil
}

func DeleteTarget(ctx context.Context, target Target) error {
	portals, err := ListPortals(ctx, target)
	if err != nil {
		return err
	}
	for _, portal := range portals {
		err = DeletePortal(ctx, target, portal)
		if err != nil {
			return err
		}
	}

	luns, err := ListLUNs(ctx, target)
	if err != nil {
		return err
	}
	for _, lun := range luns {
		err = DeleteLUN(ctx, target, lun)
		if err != nil {
			return err
		}
	}

	err = os.Remove(path.Join("/sys/kernel/config/target/iscsi", target.IQN, fmt.Sprintf("tpgt_%d", target.ID)))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	err = os.Remove(path.Join("/sys/kernel/config/target/iscsi", target.IQN))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func UpsertTarget(ctx context.Context, target Target) error {
	err := os.MkdirAll(path.Join("/sys/kernel/config/target/iscsi"), 0o755)
	if err != nil {
		return err
	}
	err = os.MkdirAll(path.Join("/sys/kernel/config/target/iscsi", target.IQN), 0o755)
	if err != nil {
		return err
	}
	tgpPath := path.Join("/sys/kernel/config/target/iscsi", target.IQN, fmt.Sprintf("tpgt_%d", target.ID))
	err = os.MkdirAll(tgpPath, 0o755)
	if err != nil {
		return err
	}
	err = SetConfig(path.Join(tgpPath, "enable"), "1")
	if err != nil {
		return err
	}
	err = SetConfig(path.Join(tgpPath, "attrib", "authentication"), "0")
	if err != nil {
		return err
	}
	err = SetConfig(path.Join(tgpPath, "attrib", "generate_node_acls"), "1")
	if err != nil {
		return err
	}
	err = SetConfig(path.Join(tgpPath, "attrib", "demo_mode_write_protect"), "0")
	if err != nil {
		return err
	}
	return nil
}

func ListPortals(ctx context.Context, target Target) ([]string, error) {
	portals := []string{}
	entries, err := os.ReadDir(
		path.Join(
			"/sys/kernel/config/target/iscsi",
			target.IQN,
			fmt.Sprintf("tpgt_%d", target.ID),
			"np",
		),
	)
	if err != nil && !os.IsNotExist(err) {
		return portals, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		portals = append(portals, entry.Name())
	}

	return portals, nil
}

func DeletePortal(ctx context.Context, target Target, portal string) error {
	err := os.Remove(
		path.Join(
			"/sys/kernel/config/target/iscsi",
			target.IQN,
			fmt.Sprintf("tpgt_%d", target.ID),
			"np",
			portal,
		),
	)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func UpsertPortal(ctx context.Context, target Target, portal string) error {
	err := os.MkdirAll(
		path.Join(
			"/sys/kernel/config/target/iscsi",
			target.IQN,
			fmt.Sprintf("tpgt_%d", target.ID),
			"np",
			portal,
		),
		0o755,
	)
	if err != nil {
		return err
	}
	return nil
}

func ListLUNs(ctx context.Context, target Target) ([]uint16, error) {
	luns := []uint16{}
	entries, err := os.ReadDir(
		path.Join(
			"/sys/kernel/config/target/iscsi",
			target.IQN,
			fmt.Sprintf("tpgt_%d", target.ID),
			"lun",
		),
	)
	if err != nil && !os.IsNotExist(err) {
		return luns, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if !strings.HasPrefix(entry.Name(), "lun_") {
			continue
		}
		u, err := strconv.ParseUint(strings.TrimPrefix(entry.Name(), "lun_"), 10, 16)
		if err != nil {
			return luns, err
		}
		luns = append(luns, uint16(u))
	}

	return luns, nil
}

func DeleteLUN(ctx context.Context, target Target, lun uint16) error {
	lunPath := path.Join(
		"/sys/kernel/config/target/iscsi",
		target.IQN,
		fmt.Sprintf("tpgt_%d", target.ID),
		"lun",
		fmt.Sprintf("lun_%d", lun),
	)

	lunSubEntries, err := os.ReadDir(lunPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	for _, lunSubEntry := range lunSubEntries {
		stat, err := os.Lstat(path.Join(lunPath, lunSubEntry.Name()))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if stat.Mode()&os.ModeSymlink == 0 {
			continue
		}

		err = os.Remove(path.Join(lunPath, lunSubEntry.Name()))
		if err != nil {
			return err
		}
	}

	err = os.Remove(lunPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func UpsertLUN(ctx context.Context, target Target, lun uint16, backstore Backstore) error {
	lunPath := path.Join(
		"/sys/kernel/config/target/iscsi",
		target.IQN,
		fmt.Sprintf("tpgt_%d", target.ID),
		"lun",
		fmt.Sprintf("lun_%d", lun),
	)
	err := os.MkdirAll(lunPath, 0o755)
	if err != nil {
		return err
	}

	backstorePath := path.Join(
		"/sys/kernel/config/target/core",
		fmt.Sprintf("%s_%d", backstore.Type, backstore.ID),
		backstore.Name,
	)
	lunDataPath := path.Join(lunPath, backstore.Name)
	err = os.Symlink(backstorePath, lunDataPath)
	if err != nil && !os.IsExist(err) {
		return err
	}

	return nil
}
