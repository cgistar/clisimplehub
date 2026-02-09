import { t } from '../../i18n/index.js'

export function grokAccountEditModalTemplate() {
  return `
    <div id="grokAccountEditModal" class="modal">
      <div class="modal-content" style="max-width: 500px;">
        <div class="modal-header">
          <h2>${t('grok.editAccountTitle')}</h2>
          <button class="modal-close" onclick="closeGrokAccountEditModal()">&times;</button>
        </div>
        <div class="modal-body">
          <div class="form-group">
            <label>${t('grok.ssoToken')}</label>
            <input type="text" id="editGrokSsoToken" readonly style="opacity: 0.6;">
          </div>
          <div class="form-group">
            <label>${t('grok.tier')}</label>
            <select id="editGrokTier">
              <option value="basic">Basic</option>
              <option value="super">Super</option>
            </select>
          </div>
          <div class="form-group">
            <label>${t('grok.proxyUrl')} (${t('common.optional')})</label>
            <input type="text" id="editGrokProxyUrl" placeholder="${t('grok.proxyUrlPlaceholder')}">
          </div>
          <div class="form-group">
            <label>${t('grok.weight')}</label>
            <input type="number" id="editGrokWeight" min="0" placeholder="1">
            <small>${t('grok.weightHelp')}</small>
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn btn-secondary" onclick="closeGrokAccountEditModal()">${t('common.cancel')}</button>
          <button class="btn btn-primary" onclick="saveGrokAccountEdit()">${t('settings.save')}</button>
        </div>
      </div>
    </div>`
}

export function grokAccountAddModalTemplate() {
  return `
    <div id="grokAccountAddModal" class="modal">
      <div class="modal-content" style="max-width: 500px;">
        <div class="modal-header">
          <h2>${t('grok.addAccount')}</h2>
          <button class="modal-close" onclick="closeGrokAccountAddModal()">&times;</button>
        </div>
        <div class="modal-body">
          <div class="form-group">
            <label>${t('grok.ssoToken')}</label>
            <input type="text" id="addGrokSsoToken" placeholder="${t('grok.ssoTokenPlaceholder')}">
          </div>
          <div class="form-group">
            <label>${t('grok.tier')}</label>
            <select id="addGrokTier">
              <option value="basic">Basic</option>
              <option value="super">Super</option>
            </select>
          </div>
          <div class="form-group">
            <label>${t('grok.proxyUrl')} (${t('common.optional')})</label>
            <input type="text" id="addGrokProxyUrl" placeholder="${t('grok.proxyUrlPlaceholder')}">
          </div>
          <div class="form-group">
            <label>${t('grok.weight')}</label>
            <input type="number" id="addGrokWeight" min="0" placeholder="1">
            <small>${t('grok.weightHelp')}</small>
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn btn-secondary" onclick="closeGrokAccountAddModal()">${t('common.cancel')}</button>
          <button class="btn btn-primary" onclick="saveGrokAccountAdd()">${t('settings.save')}</button>
        </div>
      </div>
    </div>`
}

export function grokGlobalConfigModalTemplate() {
  return `
    <div id="grokGlobalConfigModal" class="modal">
      <div class="modal-content" style="max-width: 500px;">
        <div class="modal-header">
          <h2>${t('grok.globalConfig')}</h2>
          <button class="modal-close" onclick="closeGrokGlobalConfigModal()">&times;</button>
        </div>
        <div class="modal-body">
          <div class="form-group">
            <label>${t('grok.rotationMode')}</label>
            <select id="grokGlobalRotationMode">
              <option value="fixed">${t('grok.rotationFixed')}</option>
              <option value="failover">${t('grok.rotationFailover')}</option>
              <option value="loadbalance">${t('grok.rotationLoadBalance')}</option>
            </select>
          </div>
          <div class="form-group">
            <label>${t('grok.proxyUrl')} (${t('common.optional')})</label>
            <input type="text" id="grokGlobalProxyUrl" placeholder="${t('grok.proxyUrlPlaceholder')}">
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn btn-secondary" onclick="closeGrokGlobalConfigModal()">${t('common.cancel')}</button>
          <button class="btn btn-primary" onclick="saveGrokGlobalConfig()">${t('settings.save')}</button>
        </div>
      </div>
    </div>`
}

export function grokBulkImportModalTemplate() {
  return `
    <div id="grokBulkImportModal" class="modal">
      <div class="modal-content" style="max-width: 600px;">
        <div class="modal-header">
          <h2>${t('grok.bulkImport')}</h2>
          <button class="modal-close" onclick="closeGrokBulkImportModal()">&times;</button>
        </div>
        <div class="modal-body">
          <div class="form-group">
            <label>${t('grok.bulkImportLabel')}</label>
            <textarea
              id="grokBulkImportTokens"
              rows="10"
              placeholder="${t('grok.bulkImportPlaceholder')}"
              style="font-family: monospace; font-size: 12px; resize: vertical;"
            ></textarea>
            <small>${t('grok.bulkImportHelp')}</small>
          </div>
          <div class="form-group">
            <label>${t('grok.tier')}</label>
            <select id="grokBulkImportTier">
              <option value="basic">Basic</option>
              <option value="super">Super</option>
            </select>
            <small>${t('grok.bulkImportTierHelp')}</small>
          </div>
          <div class="form-group">
            <label>${t('grok.proxyUrl')} (${t('common.optional')})</label>
            <input type="text" id="grokBulkImportProxyUrl" placeholder="${t('grok.proxyUrlPlaceholder')}">
          </div>
          <div id="grokBulkImportProgress" style="display: none; margin-top: 16px;">
            <div class="progress-info" style="display: flex; justify-content: space-between; margin-bottom: 8px;">
              <span id="grokBulkImportProgressText">${t('common.processing')}</span>
              <span id="grokBulkImportProgressCount" style="min-width: 60px; text-align: right;">0/0</span>
            </div>
            <div class="progress-container" style="width: 100%; height: 8px; background: var(--bg-secondary); border-radius: 4px; overflow: hidden;">
              <div id="grokBulkImportProgressBar" style="width: 0%; height: 100%; background: var(--primary); transition: width 0.3s;"></div>
            </div>
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn btn-secondary" onclick="closeGrokBulkImportModal()">${t('common.cancel')}</button>
          <button class="btn btn-primary" id="grokBulkImportBtn" onclick="executeGrokBulkImport()">${t('common.import')}</button>
        </div>
      </div>
    </div>`
}
