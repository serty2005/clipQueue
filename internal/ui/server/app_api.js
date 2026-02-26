(function () {
    async function request(path, options) {
        const response = await fetch(path, options || {});
        const contentType = response.headers.get('content-type') || '';

        let data = null;
        if (contentType.includes('application/json')) {
            data = await response.json();
        } else {
            const text = await response.text();
            data = { raw: text };
        }

        if (!response.ok) {
            const message = (data && (data.error || data.raw)) || ('HTTP ' + response.status);
            const err = new Error(message);
            err.status = response.status;
            err.data = data;
            throw err;
        }
        return data;
    }

    function postJSON(path, body) {
        return request(path, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body)
        });
    }

    window.ClipQueueAPI = {
        request,
        getConfig() { return request('/api/config'); },
        saveConfig(cfg) { return postJSON('/api/config', cfg); },
        captureHotkey() { return request('/api/hotkeys/capture', { method: 'POST' }); },
        getHistory() { return request('/api/history'); },
        copyHistoryItem(id) { return request('/api/copy?id=' + encodeURIComponent(id), { method: 'POST' }); },
        clearQueue() { return request('/api/queue/clear', { method: 'POST' }); },
        removeQueueItem(index) { return request('/api/history?index=' + encodeURIComponent(index), { method: 'DELETE' }); },
        parseLab(command) { return postJSON('/api/lab/parse', { command }); },
        buildLab(steps) { return postJSON('/api/lab/build', { steps }); },
        startSequenceRecording() { return request('/api/sequence/start', { method: 'POST' }); },
        stopSequenceRecording() { return request('/api/sequence/stop', { method: 'POST' }); },
        getSequenceStatus(last) {
            const qs = typeof last === 'number' ? ('?last=' + encodeURIComponent(last)) : '';
            return request('/api/sequence/status' + qs);
        }
    };
})();
