import { t } from '../../i18n/index.js'

export function manageModalTemplate() {
    return `
        <div id="manageModal" class="modal">
            <div class="modal-content">
                <div class="modal-header">
                    <h2>🏢 ${t('manage.vendors')}</h2>
                    <button class="modal-close" onclick="closeManageModal()">&times;</button>
                </div>
                <div class="modal-body">
                    <div class="manage-section">
                        <div class="section-header">
                            <button class="btn btn-sm btn-primary" onclick="showVendorForm()">+ ${t('manage.addVendor')}</button>
                        </div>
                        <div class="vendor-list" id="vendorList">
                            <div class="empty-state">${t('manage.noVendors')}</div>
                        </div>
                    </div>
                </div>
            </div>
        </div>`
}

export function vendorFormModalTemplate() {
    return `
        <div id="vendorFormModal" class="modal">
            <div class="modal-content">
                <div class="modal-header">
                    <h2 id="vendorFormTitle">${t('manage.addVendor')}</h2>
                    <button class="modal-close" onclick="closeVendorForm()">&times;</button>
                </div>
                <div class="modal-body">
                    <input type="hidden" id="vendorId">
                    <div class="form-group">
                        <label>${t('manage.vendorName')} *</label>
                        <input type="text" id="vendorName" placeholder="${t('manage.vendorNamePlaceholder')}">
                    </div>
                    <div class="form-group">
                        <label>${t('manage.homeUrl')} *</label>
                        <input type="text" id="vendorHomeUrl" placeholder="${t('manage.homeUrlPlaceholder')}">
                    </div>
                    <div class="form-group">
                        <label>${t('manage.apiUrl')} *</label>
                        <input type="text" id="vendorApiUrl" placeholder="${t('manage.apiUrlPlaceholder')}">
                    </div>
                    <div class="form-group">
                        <label>${t('manage.remark')}</label>
                        <input type="text" id="vendorRemark" placeholder="${t('manage.remarkPlaceholder')}">
                    </div>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-danger" id="deleteVendorBtn" onclick="deleteVendor()" style="margin-right:auto;display:none;">${t('manage.delete')}</button>
                    <button class="btn btn-secondary" onclick="closeVendorForm()">${t('manage.cancel')}</button>
                    <button class="btn btn-primary" onclick="saveVendor()">${t('manage.save')}</button>
                </div>
            </div>
        </div>`
}
