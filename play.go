package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/sapslaj/shortrack/pkg/telemetry"
)

const (
	KB = 1024
	MB = 1024 * 1024
	GB = 1024 * 1024 * 1024
)

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

func PlayUp(ctx context.Context, cmd *cli.Command) error {
	logger := telemetry.NewLogger()

	name := "test2"
	hostname := "iscsi-test"
	size := int64(2 * GB)
	iqn := fmt.Sprintf("iqn.2003-01.xyz.sapslaj.shortrack.%s:%s", hostname, name)
	fileioid := 2
	portal := "0.0.0.0:3260"

	//
	// sparse file creation
	device := fmt.Sprintf("/srv/%s.img", name)
	logger.Info(fmt.Sprintf("generating %s", device))
	fd, err := os.Create(device)
	if err != nil {
		logger.Error("error creating file", telemetry.Error(err))
		os.Exit(1)
	}
	_, err = fd.Seek(size-1, 0)
	if err != nil {
		logger.Error("failed to seek", telemetry.Error(err))
		os.Exit(1)
	}
	_, err = fd.Write([]byte{0})
	if err != nil {
		logger.Error("write failed", telemetry.Error(err))
		os.Exit(1)
	}
	err = fd.Close()
	if err != nil {
		logger.Error("failed to close file", telemetry.Error(err))
		os.Exit(1)
	}
	logger.Info(fmt.Sprintf("sparse file written to %s", device))

	//
	// fileio backstore setup
	kernelConfigDir := "/sys/kernel/config/target"
	iscsiDir := path.Join(kernelConfigDir, "iscsi")
	err = os.MkdirAll(iscsiDir, 0o755)
	if err != nil {
		logger.Error(fmt.Sprintf("failed to mkdir %s", iscsiDir), telemetry.Error(err))
		os.Exit(1)
	}
	coreDir := path.Join(kernelConfigDir, "core")
	backstoreDir := path.Join(coreDir, fmt.Sprintf("fileio_%d", fileioid))
	backstoreDataDir := path.Join(backstoreDir, name)
	err = os.MkdirAll(backstoreDataDir, 0o755)
	if err != nil {
		logger.Error(fmt.Sprintf("failed to mkdir %s", backstoreDataDir), telemetry.Error(err))
		os.Exit(1)
	}
	err = WriteConfig(
		path.Join(backstoreDataDir, "control"),
		fmt.Appendf(nil, "fd_dev_name=%s", device),
	)
	if err != nil {
		logger.Error("failed to set fb_dev_name", telemetry.Error(err))
		os.Exit(1)
	}
	err = WriteConfig(
		path.Join(backstoreDataDir, "control"),
		fmt.Appendf(nil, "fd_dev_size=%d", size),
	)
	if err != nil {
		logger.Error("failed to set fb_dev_size", telemetry.Error(err))
		os.Exit(1)
	}
	err = WriteConfig(
		path.Join(backstoreDataDir, "attrib", "emulate_write_cache"),
		[]byte("1"),
	)
	if err != nil {
		logger.Error("failed to set emulate_write_cache", telemetry.Error(err))
		os.Exit(1)
	}
	err = WriteConfig(
		path.Join(backstoreDataDir, "wwn", "vpd_unit_serial"),
		[]byte(name),
	)
	if err != nil {
		logger.Error("failed to set vpd_unit_serial", telemetry.Error(err))
		os.Exit(1)
	}
	err = WriteConfig(
		path.Join(backstoreDataDir, "enable"),
		[]byte("1"),
	)
	if err != nil {
		logger.Error("failed to enable backstore", telemetry.Error(err))
		os.Exit(1)
	}
	logger.Info("set up fileio backstore")

	//
	// target portal group creation
	iqnDir := path.Join(iscsiDir, iqn)
	err = os.MkdirAll(iqnDir, 0o755)
	if err != nil {
		logger.Error(fmt.Sprintf("failed to mkdir %s", iqnDir), telemetry.Error(err))
		os.Exit(1)
	}
	tpgtDir := path.Join(iqnDir, "tpgt_65535")
	err = os.MkdirAll(tpgtDir, 0o755)
	if err != nil {
		logger.Error(fmt.Sprintf("failed to mkdir %s", tpgtDir), telemetry.Error(err))
		os.Exit(1)
	}
	logger.Info("created target portal group")

	//
	// LUN
	tpgtLunsDir := path.Join(tpgtDir, "lun")
	lunDir := path.Join(tpgtLunsDir, "lun_0")
	err = os.MkdirAll(lunDir, 0o755)
	if err != nil {
		logger.Error(fmt.Sprintf("failed to mkdir %s", lunDir), telemetry.Error(err))
		os.Exit(1)
	}
	lunDataDir := path.Join(lunDir, name)
	err = os.Symlink(backstoreDataDir, lunDataDir)
	if err != nil {
		logger.Error(fmt.Sprintf("failed to symlink %s to %s", backstoreDataDir, lunDataDir), telemetry.Error(err))
		os.Exit(1)
	}
	err = WriteConfig(path.Join(tpgtDir, "enable"), []byte("1"))
	if err != nil {
		logger.Error("failed to enable target portal group LUN", telemetry.Error(err))
		os.Exit(1)
	}
	logger.Info("Enabled LUN 0")

	//
	// portal creation
	portalDir := path.Join(tpgtDir, "np", portal)
	err = os.MkdirAll(portalDir, 0o755)
	if err != nil {
		logger.Error(fmt.Sprintf("failed to mkdir %s", portalDir), telemetry.Error(err))
		os.Exit(1)
	}
	logger.Info(fmt.Sprintf("created portal %s", portal))

	//
	// target portal group configuration
	err = WriteConfig(path.Join(tpgtDir, "attrib", "authentication"), []byte("0"))
	if err != nil {
		logger.Error("failed to disable authentication", telemetry.Error(err))
		os.Exit(1)
	}
	err = WriteConfig(path.Join(tpgtDir, "attrib", "generate_node_acls"), []byte("0"))
	if err != nil {
		logger.Error("failed to enable generate_node_acls", telemetry.Error(err))
		os.Exit(1)
	}
	err = WriteConfig(path.Join(tpgtDir, "attrib", "demo_mode_write_protect"), []byte("0"))
	if err != nil {
		logger.Error("failed to disable demo_mode_write_protect", telemetry.Error(err))
		os.Exit(1)
	}

	logger.Info(fmt.Sprintf("target %s, portal %s has been created", iqn, portal))
	return nil
}

func PlayDown(ctx context.Context, cmd *cli.Command) error {
	logger := telemetry.NewLogger()

	iqnEntries, err := os.ReadDir("/sys/kernel/config/target/iscsi")
	if err != nil && !os.IsNotExist(err) {
		logger.Error(
			"failed to list iqns",
			telemetry.Error(err),
		)
	}

	for _, iqnEntry := range iqnEntries {
		if !iqnEntry.IsDir() {
			continue
		}
		if !strings.HasPrefix(iqnEntry.Name(), "iqn.") {
			continue
		}
		iqn := iqnEntry.Name()
		iqnDir := path.Join("/sys/kernel/config/target/iscsi", iqn)

		tpgtEntries, err := os.ReadDir(iqnDir)
		if err != nil {
			logger.Error(
				"failed to list tpgs",
				slog.String("iqn", iqn),
				telemetry.Error(err),
			)
			continue
		}

		for _, tpgtEntry := range tpgtEntries {
			if !tpgtEntry.IsDir() {
				continue
			}
			if !strings.HasPrefix(tpgtEntry.Name(), "tpgt_") {
				continue
			}
			tpgt := tpgtEntry.Name()
			tpgtDir := path.Join(iqnDir, tpgt)

			npEntries, err := os.ReadDir(path.Join(tpgtDir, "np"))
			if err != nil {
				if !os.IsNotExist(err) {
					logger.Error(
						"failed to list nps",
						slog.String("iqn", iqn),
						slog.String("tpgt", tpgt),
						telemetry.Error(err),
					)
				}
				continue
			}

			for _, npEntry := range npEntries {
				if !npEntry.IsDir() {
					continue
				}
				portal := npEntry.Name()
				portalDir := path.Join(tpgtDir, "np", portal)
				err = os.Remove(portalDir)
				if err != nil {
					if !os.IsNotExist(err) {
						logger.Error(
							"failed to remove portal",
							slog.String("iqn", iqn),
							slog.String("tpgt", tpgt),
							slog.String("portal", portal),
							telemetry.Error(err),
						)
					}
					continue
				}
				logger.Info(
					"removed portal",
					slog.String("iqn", iqn),
					slog.String("tpgt", tpgt),
					slog.String("portal", portal),
				)
			}

			lunEntries, err := os.ReadDir(path.Join(tpgtDir, "lun"))
			if err != nil {
				if !os.IsNotExist(err) {
					logger.Error(
						"failed to list luns",
						slog.String("iqn", iqn),
						slog.String("tpgt", tpgt),
						telemetry.Error(err),
					)
				}
				continue
			}

			for _, lunEntry := range lunEntries {
				if !lunEntry.IsDir() {
					continue
				}
				lun := lunEntry.Name()
				lunDir := path.Join(tpgtDir, "lun", lun)

				lunSubEntries, err := os.ReadDir(lunDir)
				if err != nil {
					if !os.IsNotExist(err) {
						logger.Error(
							"failed to get lun info",
							slog.String("iqn", iqn),
							slog.String("tpgt", tpgt),
							slog.String("lun", lun),
							telemetry.Error(err),
						)
					}
					continue
				}

				for _, lunSubEntry := range lunSubEntries {
					stat, err := os.Lstat(path.Join(lunDir, lunSubEntry.Name()))
					if err != nil {
						logger.Error(
							"failed to get lun backstore info",
							slog.String("iqn", iqn),
							slog.String("tpgt", tpgt),
							slog.String("lun", lun),
							slog.String("lunbackstore", lunSubEntry.Name()),
							telemetry.Error(err),
						)
						continue
					}
					if stat.Mode()&os.ModeSymlink == 0 {
						continue
					}

					err = os.Remove(path.Join(lunDir, lunSubEntry.Name()))
					if err != nil {
						logger.Error(
							"failed to remove lun backstore symlink",
							slog.String("iqn", iqn),
							slog.String("tpgt", tpgt),
							slog.String("lun", lun),
							slog.String("lundata", lunSubEntry.Name()),
							telemetry.Error(err),
						)
						continue
					}

					logger.Info(
						"removed lun backstore symlink",
						slog.String("iqn", iqn),
						slog.String("tpgt", tpgt),
						slog.String("lun", lun),
						slog.String("lunbackstore", lunSubEntry.Name()),
					)
				}

				err = os.Remove(lunDir)
				if err != nil {
					if !os.IsNotExist(err) {
						logger.Error(
							"failed to remove lun",
							slog.String("iqn", iqn),
							slog.String("tpgt", tpgt),
							slog.String("lun", lun),
							telemetry.Error(err),
						)
					}
					continue
				}

				logger.Info(
					"removed lun",
					slog.String("iqn", iqn),
					slog.String("tpgt", tpgt),
					slog.String("lun", lun),
				)
			}

			err = os.Remove(tpgtDir)
			if err != nil {
				if !os.IsNotExist(err) {
					logger.Error(
						"failed to remove tpg",
						slog.String("iqn", iqn),
						slog.String("tpgt", tpgt),
						telemetry.Error(err),
					)
				}
				continue
			}

			logger.Info(
				"removed tpg",
				slog.String("iqn", iqn),
				slog.String("tpgt", tpgt),
			)
		}

		err = os.Remove(iqnDir)
		if err != nil {
			if !os.IsNotExist(err) {
				logger.Error(
					"failed to remove iqn",
					slog.String("iqn", iqn),
					telemetry.Error(err),
				)
			}
			continue
		}

		logger.Info(
			"removed iqn",
			slog.String("iqn", iqn),
		)

	}

	backstoreEntries, err := os.ReadDir("/sys/kernel/config/target/core")
	if err != nil && !os.IsNotExist(err) {
		logger.Error(
			"failed to list backstores",
			telemetry.Error(err),
		)
	}

	for _, backstoreEntry := range backstoreEntries {
		if !backstoreEntry.IsDir() {
			continue
		}
		if backstoreEntry.Name() == "alua" {
			continue
		}
		backstore := backstoreEntry.Name()
		backstoreDir := path.Join("/sys/kernel/config/target/core", backstore)

		dataEntries, err := os.ReadDir(path.Join(backstoreDir))
		if err != nil {
			if !os.IsNotExist(err) {
				logger.Error(
					"failed to list backstores data dirs",
					slog.String("backstore", backstore),
					telemetry.Error(err),
				)
			}
			continue
		}

		for _, dataEntry := range dataEntries {
			if !dataEntry.IsDir() {
				continue
			}
			err = os.Remove(path.Join(backstoreDir, dataEntry.Name()))
			if err != nil {
				if !os.IsNotExist(err) {
					logger.Error(
						"failed to remove backstore data dir",
						slog.String("backstore", backstore),
						slog.String("datadir", dataEntry.Name()),
						telemetry.Error(err),
					)
				}
				continue
			}

			logger.Info(
				"removed backstore data dir",
				slog.String("backstore", backstore),
				slog.String("datadir", dataEntry.Name()),
			)
		}

		err = os.Remove(backstoreDir)
		if err != nil {
			if !os.IsNotExist(err) {
				logger.Error(
					"failed to remove backstore",
					slog.String("backstore", backstore),
					telemetry.Error(err),
				)
			}
			continue
		}

		logger.Info(
			"removed backstore",
			slog.String("backstore", backstore),
		)
	}

	diskFileEntries, err := os.ReadDir("/srv")
	if err != nil && !os.IsNotExist(err) {
		logger.Error(
			"failed to list disk files",
			telemetry.Error(err),
		)
	}

	for _, diskFileEntry := range diskFileEntries {
		if diskFileEntry.IsDir() {
			continue
		}
		if !strings.HasSuffix(diskFileEntry.Name(), ".img") {
			continue
		}

		err = os.Remove(path.Join("/srv", diskFileEntry.Name()))
		if err != nil {
			if !os.IsNotExist(err) {
				logger.Error(
					"failed to remove disk file",
					slog.String("diskfile", diskFileEntry.Name()),
					telemetry.Error(err),
				)
			}
			continue
		}

		logger.Info(
			"removed disk file",
			slog.String("diskfile", diskFileEntry.Name()),
		)

	}

	return nil
}
