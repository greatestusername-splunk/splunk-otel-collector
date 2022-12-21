# Contributing Guidelines

If you found a bug in the Ansible role, don't hesitate to submit an issue or a 
pull request.

Please make sure to add a line to the
[Ansible Changelog](https://github.com/signalfx/splunk-otel-collector/blob/main/deployments/ansible/CHANGELOG.md)
if your change affects behavior of the Ansible collection.

## Local development

Testing and validation of this Ansible role is based on 
[Ansible Molecule](https://molecule.readthedocs.io/en/latest/).


### Linux setup

Development on a Linux machine is simpler, all you need is docker. 

Make sure the following software is installed:

- [Docker](https://docs.docker.com/get-docker/)
- [Python3](https://www.python.org/downloads)

Installation steps for Linux:

1. Make sure Python 3 and [pip](https://pip.pypa.io/en/stable/installing/) are installed : `pip3 --version`
1. Make sure Docker is installed and can be managed by the current user: `docker ps`
1. Create a virtualenv environment to run safely: `python3 -m venv venv`
1. Activate the venv: `source venv/bin/activate`
1. Prepend your PATH with the local venv path: `export PATH=$(pwd)/venv/bin:$PATH`
1. Install python packages: `pip3 install -r requirements-dev-linux.txt`

Use the following arguments with every molecule command 
`--base-config ./molecule/config/docker.yml`.

To setup test docker containers:
```sh
molecule --base-config ./molecule/config/docker.yml create
```

To apply Molecule playbooks:
```sh
molecule --base-config ./molecule/config/docker.yml converge
```

To run the full test suite:
```sh
molecule --base-config ./molecule/config/docker.yml test --all
```

## Making new release of the Ansible Collection

To cut a new release of the Ansible Collection, just bump the version in
[galaxy.yml](https://github.com/signalfx/splunk-otel-collector/blob/main/deployments/ansible/galaxy.yml),
and it'll automatically build a new version of the collection and publish it to 
[Ansible Galaxy](https://galaxy.ansible.com/signalfx/splunk_otel_collector).
