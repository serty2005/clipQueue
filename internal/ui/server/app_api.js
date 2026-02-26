(function () {
    let requestImpl = async function (path, options) {
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
    };

    function setRequestImpl(fn) {
        if (typeof fn !== 'function') {
            throw new Error('transport-запрос должен быть функцией');
        }
        requestImpl = fn;
    }

    async function request(path, options) {
        return requestImpl(path, options || {});
    }

    function postJSON(path, body) {
        return request(path, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body)
        });
    }

    function nativeAvailable() {
        return typeof window.cqNativeGetConfig === 'function';
    }

    function nativeAPI() {
        return {
            request,
            getConfig() { return window.cqNativeGetConfig(); },
            saveConfig(cfg) { return window.cqNativeSaveConfig(cfg); },
            captureHotkey() { return window.cqNativeCaptureHotkey(); },
            getHistory() { return window.cqNativeGetHistory(); },
            getQueueState() { return window.cqNativeGetQueueState(); },
            toggleQueue() { return window.cqNativeToggleQueue(); },
            toggleQueueOrder() { return window.cqNativeToggleQueueOrder(); },
            copyHistoryItem(id) { return window.cqNativeCopyHistoryItem(id); },
            clearQueue() { return window.cqNativeClearQueue(); },
            removeQueueItem(index) { return window.cqNativeRemoveQueueItem(index); },
            parseLab(command) { return window.cqNativeParseLab(command); },
            buildLab(steps) { return window.cqNativeBuildLab(steps); },
            startSequenceRecording() { return window.cqNativeStartSequenceRecording(); },
            stopSequenceRecording() { return window.cqNativeStopSequenceRecording(); },
            getSequenceStatus(last) { return window.cqNativeGetSequenceStatus(typeof last === 'number' ? last : 30); }
        };
    }

    function httpAPI() {
        return {
            request,
            getConfig() { return request('/api/config'); },
            saveConfig(cfg) { return postJSON('/api/config', cfg); },
            captureHotkey() { return request('/api/hotkeys/capture', { method: 'POST' }); },
            getHistory() { return request('/api/history'); },
            getQueueState() { return request('/api/queue/state'); },
            toggleQueue() { return request('/api/queue/toggle', { method: 'POST' }); },
            toggleQueueOrder() { return request('/api/queue/order/toggle', { method: 'POST' }); },
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
    }

    window.ClipQueueAPI = nativeAvailable() ? nativeAPI() : httpAPI();
    window.ClipQueueTransport = { request, postJSON, setRequestImpl };
})();

