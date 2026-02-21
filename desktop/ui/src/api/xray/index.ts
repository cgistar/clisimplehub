import * as App from '../../../wailsjs/go/main/App'
import type {
  XrayConfig,
  XrayNode,
  XrayRefreshResult,
  XraySpeedTestResult,
  XrayStatus
} from '@/types/xray'

export const xrayApi = {
  async isAvailable(): Promise<boolean> {
    return App.IsXRayAvailable()
  },

  async getStatus(): Promise<XrayStatus> {
    return App.GetXRayStatus()
  },

  async getConfig(): Promise<XrayConfig> {
    return App.GetXRayConfig()
  },

  async getNodes(): Promise<XrayNode[]> {
    return App.GetXRayNodes()
  },

  async getNodeConfig(nodeTag: string): Promise<string> {
    return App.GetXRayNodeConfig(nodeTag)
  },

  async start(): Promise<void> {
    await App.StartXRay()
  },

  async stop(): Promise<void> {
    await App.StopXRay()
  },

  async selectNode(nodeName: string): Promise<void> {
    await App.SelectXRayNode(nodeName)
  },

  async testNode(nodeName: string): Promise<XraySpeedTestResult> {
    return App.TestXRayNode(nodeName)
  },

  async refreshSubscriptions(): Promise<XrayRefreshResult> {
    return App.RefreshXRaySubscriptions()
  },

  async addSubscription(name: string, url: string): Promise<void> {
    await App.AddXRaySubscription(name, url)
  },

  async removeSubscription(id: string): Promise<void> {
    await App.RemoveXRaySubscription(id)
  },

  async toggleSubscription(id: string): Promise<void> {
    await App.ToggleXRaySubscription(id)
  },

  async refreshSingleSubscription(id: string): Promise<XrayRefreshResult> {
    return App.RefreshSingleXRaySubscription(id)
  },

  async activateSubscription(id: string): Promise<void> {
    await App.ActivateXRaySubscription(id)
  },

  async setActiveSubscription(id: string): Promise<void> {
    await App.SetActiveXRaySubscription(id)
  },

  async updateSubscriptionSelectedNode(id: string, nodeName: string): Promise<void> {
    await App.UpdateXRaySubscriptionSelectedNode(id, nodeName)
  },

  async updateSubscription(id: string, name: string, url: string): Promise<void> {
    await App.UpdateXRaySubscription(id, name, url)
  },

  async parseNodesForSubscription(id: string, content: string): Promise<XrayNode[]> {
    return App.ParseXRayNodesForSubscription(id, content) as Promise<XrayNode[]>
  },

  async replaceSubscriptionNodes(id: string, nodes: XrayNode[], selectedNode: string): Promise<void> {
    await App.ReplaceXRaySubscriptionNodes(id, JSON.stringify(nodes), selectedNode)
  },

  async removeNodeFromSubscription(id: string, nodeName: string): Promise<void> {
    await App.RemoveXRayNodeFromSubscription(id, nodeName)
  },

  async saveConfig(config: XrayConfig): Promise<void> {
    await App.SaveXRayConfig(JSON.stringify(config))
  }
}
