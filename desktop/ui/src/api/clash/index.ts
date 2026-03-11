import * as App from '../../../wailsjs/go/main/App'
import type {
  ClashConfig,
  ClashNode,
  ClashRefreshResult,
  ClashSpeedTestResult,
  ClashStatus
} from '@/types/clash'

export const clashApi = {
  async isAvailable(): Promise<boolean> {
    return App.IsClashAvailable()
  },

  async getStatus(): Promise<ClashStatus> {
    return App.GetClashStatus()
  },

  async getConfig(): Promise<ClashConfig> {
    return App.GetClashConfig()
  },

  async reloadConfigFromDisk(): Promise<void> {
    await App.ReloadClashConfig()
  },

  async getNodes(): Promise<ClashNode[]> {
    return App.GetClashNodes()
  },

  async getNodeConfig(nodeTag: string): Promise<string> {
    return App.GetClashNodeConfig(nodeTag)
  },

  async start(): Promise<void> {
    await App.StartClash()
  },

  async stop(): Promise<void> {
    await App.StopClash()
  },

  async selectNode(nodeName: string): Promise<void> {
    await App.SelectClashNode(nodeName)
  },

  async testNode(nodeName: string): Promise<ClashSpeedTestResult> {
    return App.TestClashNode(nodeName)
  },

  async testNodeTCP(nodeName: string): Promise<ClashSpeedTestResult> {
    return App.TestClashNodeTCP(nodeName)
  },

  async cancelSpeedTests(): Promise<void> {
    await App.CancelClashSpeedTests()
  },

  async refreshSubscriptions(): Promise<ClashRefreshResult> {
    return App.RefreshClashSubscriptions()
  },

  async addSubscription(name: string, url: string): Promise<void> {
    await App.AddClashSubscription(name, url)
  },

  async removeSubscription(id: string): Promise<void> {
    await App.RemoveClashSubscription(id)
  },

  async toggleSubscription(id: string): Promise<void> {
    await App.ToggleClashSubscription(id)
  },

  async refreshSingleSubscription(id: string): Promise<ClashRefreshResult> {
    return App.RefreshSingleClashSubscription(id)
  },

  async activateSubscription(id: string): Promise<void> {
    await App.ActivateClashSubscription(id)
  },

  async setActiveSubscription(id: string): Promise<void> {
    await App.SetActiveClashSubscription(id)
  },

  async setDialerProxySubscription(id: string): Promise<void> {
    await App.SetClashDialerProxySubscription(id)
  },

  async updateSubscriptionSelectedNode(id: string, nodeName: string): Promise<void> {
    await App.UpdateClashSubscriptionSelectedNode(id, nodeName)
  },

  async updateSubscription(id: string, name: string, url: string): Promise<void> {
    await App.UpdateClashSubscription(id, name, url)
  },

  async parseNodesForSubscription(id: string, content: string): Promise<ClashNode[]> {
    return App.ParseClashNodesForSubscription(id, content) as Promise<ClashNode[]>
  },

  async replaceSubscriptionNodes(id: string, nodes: ClashNode[], selectedNode: string): Promise<void> {
    await App.ReplaceClashSubscriptionNodes(id, JSON.stringify(nodes), selectedNode)
  },

  async removeNodeFromSubscription(id: string, nodeName: string): Promise<void> {
    await App.RemoveClashNodeFromSubscription(id, nodeName)
  },

  async saveConfig(config: ClashConfig): Promise<void> {
    await App.SaveClashConfig(JSON.stringify(config))
  }
}
