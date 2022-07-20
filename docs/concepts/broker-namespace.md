# `Namespace` brokering

To sync namespaced resources from a source to a target cluster,
a deterministic mapping between a source and target `Namespace` has to exist.

In the initial version of the `partitionlet`, `Namespace` syncing was done
to only one target namespace created upfront and specified to the controller
via command line flags. Resources synced into this `Namespace` got a name
following the pattern of `<source-namespace-name>--<resource-name>`.

This approach posed multiple problems:
* The sync names already were pretty long, but syncing them down
  multiple layers made them lengthier by appending the entire previous
  prefix as well. Names like `default--default--foo` were difficult to see
  through.
* Namespace isolation from the top was not respected anymore. Of course,
  lower level clusters are implementors and thus have any privilege, however
  as by the principle of least privilege and to make observability easier
  this approach also didn't cut it.

To resolve these issues, a dynamically generated target `Namespace` has
to be created for each source `Namespace` that is in-use.
If there is a user for a source `Namespace`, a target `Namespace` with
`metadata.generateName` set to the source `Namespace` name (or the
parent controller of that namespace to simplify tracking down grandparent
namespaces) is created.

Currently, the target `Namespace` will only be deleted if the source
`Namespace` is deleted.
