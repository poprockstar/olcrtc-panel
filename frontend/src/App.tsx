type Locale = "en" | "ru";

const copy: Record<Locale, {
  eyebrow: string;
  title: string;
  status: string;
  next: string;
}> = {
  en: {
    eyebrow: "OlcRTC Panel",
    title: "Local VPS control panel",
    status: "Core API, auth, and client management endpoints are available.",
    next: "Operator screens remain scheduled for the UI phase; use the secured API for current management."
  },
  ru: {
    eyebrow: "OlcRTC Panel",
    title: "Локальная панель VPS",
    status: "Доступны основные API, авторизация и управление клиентами.",
    next: "Операторские экраны запланированы для UI-фазы; сейчас управление выполняется через защищенный API."
  }
};

export function App() {
  const locale = getInitialLocale();
  const text = copy[locale];

  return (
    <main className="shell">
      <section className="panel" aria-labelledby="app-title">
        <p className="eyebrow">{text.eyebrow}</p>
        <h1 id="app-title">{text.title}</h1>
        <p className="status">{text.status}</p>
        <p>{text.next}</p>
      </section>
    </main>
  );
}

function getInitialLocale(): Locale {
  return navigator.language.toLowerCase().startsWith("ru") ? "ru" : "en";
}
