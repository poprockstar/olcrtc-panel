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
    status: "Phase 1 skeleton is running.",
    next: "First-run setup, login, and client management arrive in later phases."
  },
  ru: {
    eyebrow: "OlcRTC Panel",
    title: "Локальная панель VPS",
    status: "Скелет Phase 1 запущен.",
    next: "Первичная настройка, вход и управление клиентами появятся в следующих фазах."
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
