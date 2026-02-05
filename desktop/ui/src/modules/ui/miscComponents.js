import { t } from '../../i18n/index.js'

export function cliConfigModalTemplate() {
    return `
        <div id="cliConfigModal" class="modal">
            <!-- Content will be dynamically generated -->
        </div>`
}

export function errorToastTemplate() {
    return `
        <div id="errorToast" class="error-toast">
            <span id="errorMessage"></span>
        </div>`
}
