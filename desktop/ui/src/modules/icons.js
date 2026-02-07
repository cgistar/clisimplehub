/**
 * Lucide 图标工具模块
 * 提供便捷的图标创建和替换功能
 */
import * as icons from 'lucide-static'

// 图标映射表
const iconMap = {
    power: 'Power',
    copy: 'Copy',
    eye: 'Eye',
    trash: 'Trash2',
    refresh: 'RotateCw',
    chart: 'BarChart3',
    battery: 'BatteryFull',
    plus: 'Plus',
    close: 'X',
    check: 'Check',
    alert: 'AlertCircle',
    info: 'Info',
    chevronDown: 'ChevronDown',
    settings: 'Settings',
    globe: 'Globe',
    home: 'Home',
    database: 'Database',
    activity: 'Activity',
    users: 'Users',
    file: 'FileText',
    download: 'Download',
    upload: 'Upload',
    search: 'Search',
    filter: 'Filter',
    refreshCw: 'RefreshCw',
    save: 'Save',
    edit: 'Edit',
    externalLink: 'ExternalLink',
    building: 'Building',
    key: 'Key',
    hammer: 'Hammer',
    terminal: 'Terminal',
    monitor: 'Monitor',
    list: 'List',
    zap: 'Zap',
    clock: 'Clock',
    server: 'Server'
}

/**
 * 创建 SVG 图标元素
 * @param {string} name - 图标名称
 * @param {Object} options - 配置选项
 * @param {number} options.size - 图标大小，默认 16
 * @param {string} options.color - 图标颜色，默认 'currentColor'
 * @param {string} options.strokeWidth - 线条宽度，默认 2
 * @param {string} options.className - 额外的 CSS 类名
 * @returns {string} SVG HTML 字符串
 */
export function createIcon(name, options = {}) {
    const {
        size = 16,
        color = 'currentColor',
        strokeWidth = 2,
        className = ''
    } = options

    const iconName = iconMap[name]
    if (!iconName || !icons[iconName]) {
        console.warn(`Icon "${name}" not found`)
        return ''
    }

    // lucide-static 返回 SVG 字符串
    let svg = icons[iconName]

    // 替换默认属性
    svg = svg.replace(/width="\d+"/, `width="${size}"`)
    svg = svg.replace(/height="\d+"/, `height="${size}"`)
    svg = svg.replace(/stroke="[^"]*"/, `stroke="${color}"`)
    svg = svg.replace(/stroke-width="\d+"/, `stroke-width="${strokeWidth}"`)

    if (className) {
        svg = svg.replace(/<svg/, `<svg class="${className}"`)
    }

    return svg
}

/**
 * 替换 DOM 中的图标占位符
 * 查找所有带有 data-icon 属性的元素并替换为对应的 lucide 图标
 */
export function replaceIcons() {
    const elements = document.querySelectorAll('[data-icon]')
    elements.forEach(el => {
        const iconName = el.getAttribute('data-icon')
        const size = parseInt(el.getAttribute('data-icon-size')) || 16
        const color = el.getAttribute('data-icon-color') || 'currentColor'
        const strokeWidth = parseInt(el.getAttribute('data-icon-stroke')) || 2

        const iconHtml = createIcon(iconName, { size, color, strokeWidth })
        if (iconHtml) {
            el.innerHTML = iconHtml
            el.style.display = 'inline-flex'
            el.style.alignItems = 'center'
            el.style.justifyContent = 'center'
        }
    })
}

/**
 * 初始化图标系统
 * 在 DOM 加载完成后自动替换图标
 */
export function initIcons() {
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', replaceIcons)
    } else {
        replaceIcons()
    }
}

// 导出图标映射供外部使用
export { iconMap }
