// Copyright 2022 Antrea Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package agent

import (
	"fmt"
	"net"
	"strings"
	"testing"

	mock "github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"antrea.io/antrea/pkg/agent/config"
	"antrea.io/antrea/pkg/agent/interfacestore"
	"antrea.io/antrea/pkg/ovs/ovsconfig"
	ovsconfigtest "antrea.io/antrea/pkg/ovs/ovsconfig/testing"
)

func mockSetInterfaceMTU(returnErr error) func() {
	return func() {}
}

func mockGetInterfaceByName(ipDevice *net.Interface) func() {
	prevGetInterfaceByName := getInterfaceByName
	getInterfaceByName = func(name string) (*net.Interface, error) {
		return ipDevice, nil
	}
	return func() { getInterfaceByName = prevGetInterfaceByName }
}

func TestPrepareOVSBridgeForK8sNode(t *testing.T) {
	macAddr, _ := net.ParseMAC("00:00:5e:00:53:01")
	_, nodeIPNet, _ := net.ParseCIDR("192.168.10.10/24")
	ipDevice := &net.Interface{
		Index:        10,
		MTU:          1500,
		Name:         "ens160",
		HardwareAddr: macAddr,
	}
	datapathID := "0000" + strings.Replace(macAddr.String(), ":", "", -1)
	nodeConfig := &config.NodeConfig{
		UplinkNetConfig: new(config.AdapterNetConfig),
		NodeIPv4Addr:    nodeIPNet,
	}

	tests := []struct {
		name                        string
		connectUplinkToBridge       bool
		expectedCalls               func(m *ovsconfigtest.MockOVSBridgeClient)
		expectedHostInterfaceOFPort uint32
		expectedUplinkOFPort        uint32
		expectedErr                 string
	}{
		{
			name: "connectUplinkToBridge is false, do nothing",
		},
		{
			name:                  "failed to set datapath_id",
			connectUplinkToBridge: true,
			expectedCalls: func(m *ovsconfigtest.MockOVSBridgeClient) {
				m.EXPECT().SetDatapathID(datapathID).Return(ovsconfig.InvalidArgumentsError("unable to set datapath_id"))
			},
			expectedErr: fmt.Sprintf("failed to set datapath_id %s: err=unable to set datapath_id", datapathID),
		},
		{
			name:                  "local port does not exist, allocate it",
			connectUplinkToBridge: true,
			expectedCalls: func(m *ovsconfigtest.MockOVSBridgeClient) {
				m.EXPECT().SetDatapathID(datapathID).Return(nil)
				m.EXPECT().GetOFPort(ipDevice.Name, false).Return(int32(0), ovsconfig.InvalidArgumentsError("interface not found"))
				m.EXPECT().AllocateOFPort(config.UplinkOFPort).Return(int32(2), nil)
				m.EXPECT().AllocateOFPort(config.UplinkOFPort).Return(int32(3), nil)
			},
			expectedUplinkOFPort:        2,
			expectedHostInterfaceOFPort: 3,
		},
		{
			name:                  "uplink interface found",
			connectUplinkToBridge: true,
			expectedCalls: func(m *ovsconfigtest.MockOVSBridgeClient) {
				m.EXPECT().SetDatapathID(datapathID).Return(nil)
				m.EXPECT().GetOFPort(ipDevice.Name, false).Return(int32(2), nil)
				m.EXPECT().GetOFPort(ipDevice.Name+"~", false).Return(int32(3), nil)
			},
			expectedHostInterfaceOFPort: 2,
			expectedUplinkOFPort:        3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controller := mock.NewController(t)
			mockOVSBridgeClient := ovsconfigtest.NewMockOVSBridgeClient(controller)
			store := interfacestore.NewInterfaceStore()
			initializer := newAgentInitializer(mockOVSBridgeClient, store)
			initializer.nodeType = config.K8sNode
			initializer.connectUplinkToBridge = tt.connectUplinkToBridge
			initializer.nodeConfig = nodeConfig
			defer mockGetIPNetDeviceFromIP(nodeIPNet, ipDevice)()
			defer mockGetInterfaceByName(ipDevice)()
			if tt.expectedCalls != nil {
				tt.expectedCalls(mockOVSBridgeClient)
			}
			err := initializer.prepareOVSBridgeForK8sNode()
			if tt.expectedErr != "" {
				assert.ErrorContains(t, err, tt.expectedErr)
			} else {
				require.NoError(t, err)
				if tt.connectUplinkToBridge {
					assert.Equal(t, tt.expectedUplinkOFPort, initializer.nodeConfig.UplinkNetConfig.OFPort)
					assert.Equal(t, tt.expectedHostInterfaceOFPort, initializer.nodeConfig.HostInterfaceOFPort)
				}
			}
		})
	}
}
