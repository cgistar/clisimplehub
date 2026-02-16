import { t } from '../../i18n/index.js'
import { createIcon } from '../icons.js'

export function xrayViewTemplate() {
  return `
        <div class="main-container xray-view" id="xrayView" style="display: none;">
            <div class="xray-page">
                <div class="card">
                    <div class="card-header">
                        <h2 style="display: flex; align-items: center; gap: 8px;">
                            ${createIcon('globe', { size: 16 })}
                            <span>${t('xray.title')}</span>
                            <span id="xrayStatusIcon" style="font-size: 14px;"></span>
                        </h2>
                        <div class="card-header-actions">
                            <button class="btn btn-sm btn-secondary" id="xrayStartStopBtn" style="margin-right: 8px;" title="${t('xray.start')}">
                                ${createIcon('power', { size: 14 })} <span id="xrayStartStopText">${t('xray.start')}</span>
                            </button>
                            <button class="btn btn-sm btn-secondary" style="margin-right: 8px;" onclick="showXRayConfigModal()" title="${t('xray.config')}">
                                ${createIcon('settings', { size: 14 })} ${t('xray.config')}
                            </button>
                            <button class="btn btn-sm btn-primary" onclick="showXRayAddSubscriptionModal()" title="${t('xray.addSub')}">
                                ${createIcon('plus', { size: 14 })} ${t('xray.addSub')}
                            </button>
                        </div>
                    </div>
                    <div class="xray-status-bar" id="xrayStatusBar" style="display: none;">
                    </div>
                    <div class="xray-section">
                        <h3>${t('xray.subscriptions')}</h3>
                        <div id="xraySubscriptions"></div>
                    </div>
                </div>
            </div>
        </div>`
}
