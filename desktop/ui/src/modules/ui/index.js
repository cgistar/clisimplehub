/**
 * UI initialization module - aggregates all UI components
 */
import { headerTemplate } from './header.js'
import { mainLayoutTemplate } from './mainLayout.js'
import { consolePanelTemplate } from './consolePanel.js'
import { settingsModalTemplate } from './settingsModal.js'
import { manageModalTemplate, vendorFormModalTemplate } from './manageModals.js'
import { endpointFormModalTemplate } from './endpointFormModal.js'
import { webdavModalTemplate } from './webdavModal.js'
import {
    kiroConfigModalTemplate,
    idcOrgLoginModalTemplate,
    idcDeviceFlowModalTemplate,
    socialLoginModalTemplate,
    kiroGlobalConfigModalTemplate,
    kiroAccountEditModalTemplate
} from './kiroModals.js'
import { cliConfigModalTemplate, errorToastTemplate } from './miscComponents.js'

export function initUI() {
    const app = document.getElementById('app')
    app.innerHTML = [
        headerTemplate(),
        mainLayoutTemplate(),
        consolePanelTemplate(),
        settingsModalTemplate(),
        manageModalTemplate(),
        vendorFormModalTemplate(),
        endpointFormModalTemplate(),
        cliConfigModalTemplate(),
        errorToastTemplate(),
        webdavModalTemplate(),
        kiroConfigModalTemplate(),
        idcOrgLoginModalTemplate(),
        idcDeviceFlowModalTemplate(),
        socialLoginModalTemplate(),
        kiroGlobalConfigModalTemplate(),
        kiroAccountEditModalTemplate()
    ].join('')
}
