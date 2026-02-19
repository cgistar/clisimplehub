import { t } from '../../i18n/index.js'
import { createIcon } from '../icons.js'

export function xrayConfigModalTemplate() {
  return `
        <div class="modal" id="xrayConfigModal" style="display: none;">
            <div class="modal-content">
                <div class="modal-header">
                    <h2>${t('xray.configTitle')}</h2>
                    <button class="close-btn" onclick="closeXRayConfigModal()">&times;</button>
                </div>
                <div class="modal-body">
                    <div class="form-group">
                        <label for="xraySocksListen">${t('xray.socksListen')}</label>
                        <input type="text" id="xraySocksListen" class="form-control" placeholder="127.0.0.1">
                    </div>
                    <div class="form-group">
                        <label for="xraySocksPort">${t('xray.socksPort')}</label>
                        <input type="number" id="xraySocksPort" class="form-control" placeholder="10808" min="1" max="65535">
                    </div>
                    <div class="form-group">
                        <label for="xrayLogLevel">${t('xray.logLevel')}</label>
                        <select id="xrayLogLevel" class="form-control">
                            <option value="debug">Debug</option>
                            <option value="info">Info</option>
                            <option value="warning">Warning</option>
                            <option value="error">Error</option>
                            <option value="none">None</option>
                        </select>
                    </div>
                    <div class="form-group switch-form-group">
                        <label class="switch-label-inline">${t('xray.globalProxy')}</label>
                        <label class="switch">
                            <input type="checkbox" id="xrayGlobalProxy">
                            <span class="slider"></span>
                        </label>
                        <small>${t('xray.globalProxyHelp')}</small>
                    </div>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-secondary" onclick="closeXRayConfigModal()">${t('common.cancel')}</button>
                    <button class="btn btn-primary" onclick="saveXRayConfig()">${t('settings.save')}</button>
                </div>
            </div>
        </div>

        <div class="modal" id="xrayAddSubscriptionModal" style="display: none;">
            <div class="modal-content">
                <div class="modal-header">
                    <h2>${t('xray.addSub')}</h2>
                    <button class="close-btn" onclick="closeXRayAddSubscriptionModal()">&times;</button>
                </div>
                <div class="modal-body">
                    <div class="form-group">
                        <label for="xraySubName">${t('xray.subName')}</label>
                        <input type="text" id="xraySubName" class="form-control" placeholder="${t('xray.subName')}">
                    </div>
                    <div class="form-group">
                        <label for="xraySubUrl">${t('xray.subUrl')}</label>
                        <input type="text" id="xraySubUrl" class="form-control" placeholder="${t('xray.subUrl')}">
                    </div>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-secondary" onclick="closeXRayAddSubscriptionModal()">${t('common.cancel')}</button>
                    <button class="btn btn-primary" onclick="addXRaySubscription()">${t('xray.addSub')}</button>
                </div>
            </div>
        </div>

        <div class="modal" id="xrayEditSubscriptionModal" style="display: none;">
            <div class="modal-content">
                <div class="modal-header">
                    <h2>${createIcon('edit', { size: 16 })} ${t('xray.editSub')}</h2>
                    <button class="close-btn" onclick="closeEditSubscriptionDialog()">&times;</button>
                </div>
                <div class="modal-body">
                    <div class="form-group">
                        <label for="xrayEditSubName">${t('xray.subName')}</label>
                        <input type="text" id="xrayEditSubName" class="form-control" placeholder="${t('xray.subName')}">
                    </div>
                    <div class="form-group">
                        <label for="xrayEditSubUrl">${t('xray.subUrl')}</label>
                        <input type="text" id="xrayEditSubUrl" class="form-control" placeholder="${t('xray.subUrl')}">
                    </div>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-secondary" onclick="closeEditSubscriptionDialog()">${t('common.cancel')}</button>
                    <button class="btn btn-primary" onclick="saveEditSubscription()">${t('common.save')}</button>
                </div>
            </div>
        </div>

        <div class="modal" id="xrayNodesModal" style="display: none;">
            <div class="modal-content modal-large">
                <div class="modal-header">
                    <h2>${createIcon('list', { size: 16 })} ${t('xray.manageNodes')}</h2>
                    <div style="display: flex; gap: 8px; margin-left: auto; margin-right: 8px;">
                        <button class="btn btn-sm btn-primary" onclick="showAddNodeDialog()" title="${t('xray.addNode')}">
                            ${createIcon('plus', { size: 14 })} ${t('xray.addNode')}
                        </button>
                        <button class="btn btn-sm btn-secondary" id="refreshNodesBtn" onclick="refreshCurrentSubscriptionNodes()">
                            ${createIcon('refreshCw', { size: 14 })} ${t('xray.refreshSub')}
                        </button>
                        <button class="btn btn-sm btn-secondary" id="testAllNodesBtn" onclick="testAllNodesInSubscription()">
                            ${createIcon('zap', { size: 14 })} ${t('xray.testAll')}
                        </button>
                    </div>
                    <button class="close-btn" onclick="closeSubscriptionNodesDialog()">&times;</button>
                </div>
                <div class="modal-body" style="padding: 20px;">
                    <div id="xrayNodesGrid" class="xray-nodes-grid"></div>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-secondary" onclick="closeSubscriptionNodesDialog()">${t('common.cancel')}</button>
                    <button class="btn btn-primary" onclick="saveSelectedNode()">${t('common.save')}</button>
                </div>
            </div>
        </div>

        <div class="modal" id="xrayAddNodeModal" style="display: none;">
            <div class="modal-content">
                <div class="modal-header">
                    <h2>${createIcon('plus', { size: 16 })} ${t('xray.addNodeTitle')}</h2>
                    <button class="close-btn" onclick="closeAddNodeDialog()">&times;</button>
                </div>
                <div class="modal-body">
                    <div class="form-group">
                        <textarea id="xrayAddNodeContent" class="form-control" rows="10" placeholder="${t('xray.addNodePlaceholder')}" style="font-family: monospace; font-size: 13px; resize: vertical;"></textarea>
                    </div>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-secondary" onclick="closeAddNodeDialog()">${t('common.cancel')}</button>
                    <button class="btn btn-primary" onclick="addNodeFromInput()">${t('xray.addNode')}</button>
                </div>
            </div>
        </div>`
}
