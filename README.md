# poollet
[![Pull Request Code test](https://github.com/onmetal/poollet/actions/workflows/test.yml/badge.svg?branch=main)](https://github.com/onmetal/poollet/actions/workflows/test.yml)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg?style=flat-square)](https://makeapullrequest.com)
[![GitHub License](https://img.shields.io/static/v1?label=License&message=Apache-2.0&color=blue&style=flat-square)](LICENSE)

## Overview

`poollet` is an umbrella project for `onmetal-api` pool implementors. It additionally hosts
`brokerlet`s, a category of `poollet`s that work by syncing resources to another `onmetal-api` enabled cluster.

## Installation, Usage and Development

As mentioned, `poollet` hosts multiple broker-poollets. For determining what is the correct one for your use
case, consider the following table:

|                      | Volumes | Machines | Volumes & Machines | Proxy Volumes |
|----------------------|:-------:|:--------:|:------------------:|:-------------:|
| volumebrokerlet      |    ✔    |          |                    |               |
| machinebrokerlet     |         |    ✔     |                    |               |
| partitionlet         |         |          |         ✔          |               |
| proxyvolumebrokerlet |         |          |                    |       ✔       |

Any of these brokerlets can be run using

```shell
make run-<name>
```

and deployed into the currently selected (determined by your current kubeconfig) cluster using

```shell
make deploy-<name>
```

This will apply the respective default kustomization with correct RBAC permissions.

To undeploy them, run

```shell
make undeploy-<name>
```

## Contributing

We'd love to get feedback from you. Please report bugs, suggestions or post questions by opening a GitHub issue.

## License

[Apache-2.0](LICENSE)
