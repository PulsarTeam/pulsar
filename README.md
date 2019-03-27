## Pulsar

The use of innovative consensus mechanisms based on the Ethereum architecture is divided into four layers: the base layer, the core layer, the service layer, and the application layer. The base layer provides the most basic components of the blockchain system, including P2P networks, databases, and cryptographic algorithm libraries. The core layer implements the core logic of the blockchain system, including blockchain data and state management and a new consensus mechanism (DS-POW and Conflux modules). The server layer provides external services, including virtual machine implementation, RPC interface, and smart contracts. The application layer provides end users with trusted, secure and fast blockchain applications, mainly serving users in the form of DApps.

## Building the source

For prerequisites and detailed build instructions please read the
[Installation Instructions]()
on the wiki.

Building pulsar requires both a Go (version 1.7 or later) and a C compiler.
You can install them using your favourite package manager.
Once the dependencies are installed, run

    make pulsar

or, to build the full suite of utilities:

    make all
