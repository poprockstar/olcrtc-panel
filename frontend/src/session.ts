type StoredSession = {
  username: string;
  csrfToken: string;
};

const sessionKey = "olcpanel.session";

export function loadStoredSession(): StoredSession | null {
  try {
    const raw = sessionStorage.getItem(sessionKey);
    return raw ? (JSON.parse(raw) as StoredSession) : null;
  } catch {
    return null;
  }
}

export function saveStoredSession(session: StoredSession) {
  sessionStorage.setItem(sessionKey, JSON.stringify(session));
}

export function clearStoredSession() {
  sessionStorage.removeItem(sessionKey);
}
