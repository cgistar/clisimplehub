import { t } from '../../i18n/index.js'

export function webdavModalTemplate() {
    return `
        <div id="webdavModal" class="modal">
            <div class="modal-content modal-large">
                <div class="modal-header">
                    <h2>🔄 ${t('webdav.title')}</h2>
                    <button class="modal-close" onclick="closeWebDAVModal()">×</button>
                </div>
                <div class="modal-body">
                    <!-- WebDAV Server Configuration -->
                    <div class="card-section">
                        <h3>WebDAV 服务器配置</h3>
                        <div class="form-group">
                            <label>服务器地址</label>
                            <div class="model-input-wrapper">
                                <input type="text" id="webdavServerUrl" placeholder="https://dav.example.com/backup">
                                <button class="btn btn-sm btn-secondary" onclick="testWebDAVConnection()" id="webdavTestBtn">
                                    🧪 测试
                                </button>
                            </div>
                            <small>请输入WebDAV服务器地址（支持https/http）</small>
                        </div>
                        <div class="form-row">
                            <div class="form-group">
                                <label>用户名</label>
                                <input type="text" id="webdavUsername" placeholder="用户名">
                            </div>
                            <div class="form-group">
                                <label>密码</label>
                                <input type="password" id="webdavPassword" placeholder="密码">
                            </div>
                        </div>
                        <div class="form-row" style="justify-content: flex-end;">
                            <button class="btn btn-primary" onclick="backupToWebDAV()" id="webdavBackupBtn">
                                💾 备份配置
                            </button>
                        </div>
                    </div>

                    <!-- Backup Records -->
                    <div class="card-section">
                        <h3>备份记录</h3>
                        <div class="backup-actions-bar">
                            <button class="btn btn-sm btn-secondary" onclick="loadBackupsList()">
                                🔄 刷新列表
                            </button>
                        </div>
                        <div class="webdav-backups-list" id="webdavBackupsList">
                            <div class="empty-state">暂无备份记录</div>
                        </div>
                    </div>
                </div>
            </div>
        </div>`
}
