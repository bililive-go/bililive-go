export function parseCookieString(cookie: string): Record<string, string> {
    const result: Record<string, string> = {};
    cookie.split(';').forEach((item) => {
        const part = item.trim();
        if (!part) {
            return;
        }
        const index = part.indexOf('=');
        if (index <= 0) {
            return;
        }
        const key = part.slice(0, index).trim();
        const value = part.slice(index + 1).trim();
        if (key) {
            result[key] = value;
        }
    });
    return result;
}

export function buildCookieString(cookieMap: Record<string, string>, preferredOrder: readonly string[]): string {
    const seen = new Set<string>();
    const orderedKeys = [...preferredOrder, ...Object.keys(cookieMap)];
    const parts: string[] = [];

    orderedKeys.forEach((key) => {
        if (seen.has(key)) {
            return;
        }
        seen.add(key);
        const value = cookieMap[key];
        if (value === undefined || value === null || String(value).trim() === '') {
            return;
        }
        parts.push(`${key}=${value}`);
    });

    return parts.join('; ');
}
