import type { Locale } from "./types";

export const copy = {
  en: {
    setup: "Initial setup",
    login: "Sign in",
    username: "Username",
    password: "Password",
    createAdmin: "Create admin",
    signIn: "Sign in",
    dashboard: "Dashboard",
    clients: "Clients",
    logs: "Logs",
    settings: "Settings",
    backups: "Backups",
    reload: "Reload",
    logout: "Logout",
    saveClient: "Save client",
    saveLocation: "Save location",
    saveSettings: "Save settings",
    loadSubscription: "Load subscription"
  },
  ru: {
    setup: "Первичная настройка",
    login: "Вход",
    username: "Имя пользователя",
    password: "Пароль",
    createAdmin: "Создать администратора",
    signIn: "Войти",
    dashboard: "Панель",
    clients: "Клиенты",
    logs: "Журналы",
    settings: "Настройки",
    backups: "Резерв",
    reload: "Перезагрузить",
    logout: "Выйти",
    saveClient: "Сохранить клиента",
    saveLocation: "Сохранить локацию",
    saveSettings: "Сохранить настройки",
    loadSubscription: "Загрузить подписку"
  }
} satisfies Record<Locale, Record<string, string>>;

export function browserLocale(): Locale {
  return navigator.language.toLowerCase().startsWith("ru") ? "ru" : "en";
}
