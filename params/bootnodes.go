// Copyright 2015 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package params

// MainnetBootnodes are the enode URLs of the P2P bootstrap nodes running on
// the main Ethereum network.
var MainnetBootnodes = []string{
	// Ethereum Foundation Go Bootnodes
	"enode://f4a044423beffe57456eb02c19018d645e9afff3166f6693719f4d30b3f3c361f666cac89c2e788d813cb94aa1416948bf3d4a9085c92f9825db95f182a2d964@159.138.146.38:30303",
	"enode://1d063528c9b2988120904a23bae69408637c73f9eaeb98440788936f51e114db2983a48eefa22fd15bb310313e9e28e09d4c09294a965c54ea143e19e92989ff@159.138.145.111:30303",
	"enode://0255a5e4cb9b09137a4032e8d754d026a505cf2f4d0994fe8326ae3485c047f8f6184e810f1125d24c4ac87957563a8afa025b598b5a9a1975977a9b2e8f6132@47.108.105.245:30303",
	"enode://a764d63e13951083066ceedf00c63a1d6413d3f3dff33640912f60c1ca53ffbc14786d3395725e0c663451d669b595021d56ddedda224aa04cf275095354d703@47.254.64.88:30303",
	"enode://ef93df807be5dba0ab9ddc95c0c238dc6f65e4a82bca511abdfa02ee9f85f76fbc1f72b6b664b5259bb5b80339eb93b2d6a582eec69f9772781fab894cf35079@8.208.26.176:30303",
	"enode://7545ac2d74ab35edec85afbd563183e9521b810c375bce66c47375e32ea57699e8f2c3c6e0eeae978f31b45cb308039189dc9f0981c500c52c80ec02f396da75@119.3.80.181:30303",
	"enode://b24d45dfa5b6e301fe8db23faf9398d9540aa8629d9afa621d0dcfb69e94be3759c01e49f0306a5eead59c16a0fd461e87c4ed668e31c43a04baf417fd2a2623@159.138.89.11:30303",
	"enode://d32122f0b541ebde2019c58c724d0ffb3e4e2c1f8abbe7dcd2942c08146525c09cf62e78aaa8e3ed4c6e7451aad91ea13cfe6d76e6ae052ffbab7d3c1511f3b7@47.74.86.78:30303",
}

// TestnetBootnodes are the enode URLs of the P2P bootstrap nodes running on the
// Ropsten test network.
var TestnetBootnodes = []string{
	"enode://30b7ab30a01c124a6cceca36863ece12c4f5fa68e3ba9b0b51407ccc002eeed3b3102d20a88f1c1d3c3154e2449317b8ef95090e77b312d5cc39354f86d5d606@52.176.7.10:30303",    // US-Azure geth
	"enode://865a63255b3bb68023b6bffd5095118fcc13e79dcf014fe4e47e065c350c7cc72af2e53eff895f11ba1bbb6a2b33271c1116ee870f266618eadfc2e78aa7349c@52.176.100.77:30303",  // US-Azure parity
	"enode://6332792c4a00e3e4ee0926ed89e0d27ef985424d97b6a45bf0f23e51f0dcb5e66b875777506458aea7af6f9e4ffb69f43f3778ee73c81ed9d34c51c4b16b0b0f@52.232.243.152:30303", // Parity
	"enode://94c15d1b9e2fe7ce56e458b9a3b672ef11894ddedd0c6f247e0f1d3487f52b66208fb4aeb8179fce6e3a749ea93ed147c37976d67af557508d199d9594c35f09@192.81.208.223:30303", // @gpip
}

// RinkebyBootnodes are the enode URLs of the P2P bootstrap nodes running on the
// Rinkeby test network.
var RinkebyBootnodes = []string{
	"enode://a24ac7c5484ef4ed0c5eb2d36620ba4e4aa13b8c84684e1b4aab0cebea2ae45cb4d375b77eab56516d34bfbd3c1a833fc51296ff084b770b94fb9028c4d25ccf@52.169.42.101:30303", // IE
	"enode://343149e4feefa15d882d9fe4ac7d88f885bd05ebb735e547f12e12080a9fa07c8014ca6fd7f373123488102fe5e34111f8509cf0b7de3f5b44339c9f25e87cb8@52.3.158.184:30303",  // INFURA
	"enode://b6b28890b006743680c52e64e0d16db57f28124885595fa03a562be1d2bf0f3a1da297d56b13da25fb992888fd556d4c1a27b1f39d531bde7de1921c90061cc6@159.89.28.211:30303", // AKASHA
}

// DiscoveryV5Bootnodes are the enode URLs of the P2P bootstrap nodes for the
// experimental RLPx v5 topic-discovery network.
var DiscoveryV5Bootnodes = []string{
	"enode://06051a5573c81934c9554ef2898eb13b33a34b94cf36b202b69fde139ca17a85051979867720d4bdae4323d4943ddf9aeeb6643633aa656e0be843659795007a@35.177.226.168:30303",
	"enode://0cc5f5ffb5d9098c8b8c62325f3797f56509bff942704687b6530992ac706e2cb946b90a34f1f19548cd3c7baccbcaea354531e5983c7d1bc0dee16ce4b6440b@40.118.3.223:30304",
	"enode://1c7a64d76c0334b0418c004af2f67c50e36a3be60b5e4790bdac0439d21603469a85fad36f2473c9a80eb043ae60936df905fa28f1ff614c3e5dc34f15dcd2dc@40.118.3.223:30306",
	"enode://85c85d7143ae8bb96924f2b54f1b3e70d8c4d367af305325d30a61385a432f247d2c75c45c6b4a60335060d072d7f5b35dd1d4c45f76941f62a4f83b6e75daaf@40.118.3.223:30307",
}
