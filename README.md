# partitionlet
[![Pull Request Code test](https://github.com/onmetal/partitionlet/actions/workflows/test.yml/badge.svg?branch=main)](https://github.com/onmetal/partitionlet/actions/workflows/test.yml)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg?style=flat-square)](https://makeapullrequest.com)
[![GitHub License](https://img.shields.io/static/v1?label=License&message=Apache-2.0&color=blue&style=flat-square)](LICENSE)

## Overview

`partitionlet` aggregates and syncs `MachinePool`s, `Machine`s, `Volume`s and `StoragePool`s.

## Installation, Usage and Development

partitionlet is a [kubebuilder](https://github.com/kubernetes-sigs/kubebuilder) project.
The API definitions can be found at [apis](apis).

The project also comes with a well-defined [Makefile](Makefile).
The CRDs can be deployed using

```shell
make install
```

To run the controllers locally, just run

```shell
make run
```

To deploy the controllers into the currently selected (determined by your current kubeconfig) cluster,
just run

```shell
make deploy
```

This will apply the [default kustomization)[config/default] with correct RBAC permissions.


## Contributing

We'd love to get feedback from you. Please report bugs, suggestions or post questions by opening a GitHub issue.

## License

[Apache-2.0](LICENSE)
