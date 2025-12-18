import en from './en.js';
import zhCN from './zh-CN.js';

const translations = {
    'en': en,
    'zh-CN': zhCN
};

let currentLanguage = 'en';

export function setLanguage(lang) {
    if (translations[lang]) {
        currentLanguage = lang;
    }
}

export function getLanguage() {
    return currentLanguage;
}

export function t(key) {
    const keys = key.split('.');
    let value = translations[currentLanguage];

    for (const k of keys) {
        if (value && typeof value === 'object') {
            value = value[k];
        } else {
            return key;
        }
    }

    return value || key;
}

export function getAvailableLanguages() {
    return [
        { code: 'en', name: 'English' },
        { code: 'zh-CN', name: '简体中文' }
    ];
}
