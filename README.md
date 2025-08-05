# shortrack

_Like Longhorn, but shit_

## Documentation

lol

## Reasons for existence

I have an old ahh NAS running bare Debian. I want all of my storage to be
centralized on that. So I set up NFS. But NFS is dogshit for some things (like
databases, etc.) so I really need real block storage. What are my options?

- *[Longhorn](https://longhorn.io/)* - hyperconverged (ie not centralized)
  which I explicitly don't want. But it's pretty simple to set up.
- *[Rook](https://rook.io/)/[Ceph](https://ceph.com/)* - theoretically possible
  to do what I want, but i don't have the patience or braincells to try running
  Ceph tbh.
- *[`democratic-csi`](https://github.com/democratic-csi/democratic-csi)* - I'm
  not using TrueNAS/FreeNAS, nor am I using ZFS. And the more generic drivers
  aren't block-based, require external configuration, or are built on top of some
  other complicated network file system i would rather not deal with.
- *[targetd](https://github.com/open-iscsi/targetd)* - getting close, but it's
  a fucking nightmare to run and configure. The targetd provisioner for
  Kubernetes [has been abandoned for several
  years](https://github.com/kubernetes-retired/external-storage/tree/master/iscsi/targetd).
  It also doesn't support file-backed volumes and trying to hack support for
  that in made me want to die due to how terrible the code for that project is
  (sorry, targetd maintainers, but _please_ for the love of god learn Python type
  hints and stop blindly calling functions in modules and abusing global
  variables...).

So, running out of options, I wrote my own. Which is this. It's a Kubernetes
provisioner that talks to a gRPC server running in a NAS which boops the LIO
subsystem in the kernel to poop out iSCSI targets which the Kubernetes
provisioner configures PersistentVolumes which. No fancy CSI needed!
