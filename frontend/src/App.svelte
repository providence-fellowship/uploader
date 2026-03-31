<script>
  import { onMount, onDestroy } from 'svelte';
  import { EventsOn } from '../wailsjs/runtime/runtime.js';
  import {
    OpenVideoFileDialog,
    StartUpload,
    RunDryTest,
    LoadConfig,
    SaveConfig,
    HasYouTubeToken,
    AuthenticateYouTube,
  } from '../wailsjs/go/main/App.js';

  // --- View state ---
  let view = 'upload'; // 'upload' | 'settings'

  // --- Upload form ---
  let videoPath = '';
  let title = '';
  let verse = '';
  let preacher = '';
  let uploading = false;
  let testing = false;
  let finalResult = null; // { ok, message }

  // --- Platform status cards ---
  const platforms = ['normalize', 'youtube', 'facebook', 'spotify'];
  let status = {
    normalize: { state: 'idle', message: 'Waiting…' },
    youtube:   { state: 'idle', message: 'Waiting…' },
    facebook:  { state: 'idle', message: 'Waiting…' },
    spotify:   { state: 'idle', message: 'Waiting…' },
  };

  // --- Settings ---
  let cfg = {
    youtube_client_id: '',
    youtube_client_secret: '',
    youtube_privacy: 'unlisted',
    facebook_page_id: '',
    facebook_access_token: '',
    spotify_email: '',
    spotify_password: '',
    spotify_show_url: '',
    output_dir: '',
  };
  let hasYTToken = false;
  let settingsSaved = false;
  let settingsError = '';
  let ytAuthStatus = '';

  let unsubscribe;

  onMount(async () => {
    // Load persisted config
    try {
      const loaded = await LoadConfig();
      if (loaded) cfg = { ...cfg, ...loaded };
    } catch (_) {}

    try { hasYTToken = await HasYouTubeToken(); } catch (_) {}

    // Listen for upload progress events from the backend
    unsubscribe = EventsOn('upload:progress', (data) => {
      const { step, message } = data;
      if (!status[step]) return;

      let state = 'running';
      const lower = message.toLowerCase();
      if (lower.startsWith('error')) state = 'error';
      else if (message.startsWith('✓') || lower.includes('complete') || lower.includes('success') || lower.includes('published') || lower.includes('verified') || lower.includes('passed')) state = 'done';
      else if (lower === 'queued') state = 'queued';

      status[step] = { state, message };
      status = status; // trigger reactivity
    });
  });

  onDestroy(() => {
    if (unsubscribe) unsubscribe();
  });

  async function pickFile() {
    const p = await OpenVideoFileDialog();
    if (p) videoPath = p;
  }

  async function startUpload() {
    if (!videoPath || !title || !verse || !preacher) {
      finalResult = { ok: false, message: 'Please fill in all fields and select a video file.' };
      return;
    }
    uploading = true;
    finalResult = null;
    // Reset statuses
    for (const p of platforms) {
      status[p] = { state: 'idle', message: 'Waiting…' };
    }
    status = status;

    try {
      const result = await StartUpload(videoPath, { title, verse, preacher });
      finalResult = result;
    } catch (err) {
      finalResult = { ok: false, message: String(err) };
    } finally {
      uploading = false;
    }
  }

  async function runDryTest() {
    testing = true;
    finalResult = null;
    for (const p of platforms) {
      status[p] = { state: 'idle', message: 'Waiting…' };
    }
    status = status;
    try {
      const result = await RunDryTest(videoPath);
      finalResult = result;
    } catch (err) {
      finalResult = { ok: false, message: String(err) };
    } finally {
      testing = false;
    }
  }

  async function saveSettings() {
    settingsSaved = false;
    settingsError = '';
    try {
      await SaveConfig(cfg);
      hasYTToken = await HasYouTubeToken();
      settingsSaved = true;
      setTimeout(() => settingsSaved = false, 3000);
    } catch (err) {
      settingsError = String(err);
    }
  }

  async function authenticateYouTube() {
    ytAuthStatus = 'Opening browser…';
    try {
      await AuthenticateYouTube();
      hasYTToken = await HasYouTubeToken();
      ytAuthStatus = hasYTToken ? 'Authenticated!' : 'Authentication may have failed — check browser.';
    } catch (err) {
      ytAuthStatus = 'Error: ' + String(err);
    }
    setTimeout(() => ytAuthStatus = '', 5000);
  }

  function stateClass(state) {
    return {
      idle:   'card--idle',
      queued: 'card--queued',
      running:'card--running',
      done:   'card--done',
      error:  'card--error',
    }[state] ?? 'card--idle';
  }

  function stateIcon(state) {
    return { idle: '○', queued: '◔', running: '◌', done: '✓', error: '✗' }[state] ?? '○';
  }

  const platformLabels = { normalize: 'Audio Normalization', youtube: 'YouTube', facebook: 'Facebook', spotify: 'Spotify' };
</script>

<div class="shell">
  <!-- Sidebar nav -->
  <nav class="sidebar">
    <div class="sidebar__logo">🎙️</div>
    <button class="nav-btn" class:nav-btn--active={view === 'upload'} on:click={() => view = 'upload'}>Upload</button>
    <button class="nav-btn" class:nav-btn--active={view === 'settings'} on:click={() => view = 'settings'}>Settings</button>
  </nav>

  <!-- Main content -->
  <main class="content">

    <!-- ===== UPLOAD VIEW ===== -->
    {#if view === 'upload'}
    <section class="panel">
      <h1 class="panel__title">New Sermon Upload</h1>

      <div class="form">
        <label class="form__label" for="f-title">Sermon Title</label>
        <input id="f-title" class="form__input" type="text" placeholder="e.g. Walking by Faith" bind:value={title} disabled={uploading} />

        <label class="form__label" for="f-verse">Scripture Verse</label>
        <input id="f-verse" class="form__input" type="text" placeholder="e.g. Hebrews 11:1" bind:value={verse} disabled={uploading} />

        <label class="form__label" for="f-preacher">Preacher</label>
        <input id="f-preacher" class="form__input" type="text" placeholder="e.g. Pastor John Smith" bind:value={preacher} disabled={uploading} />

        <p class="form__label">Video File (OBS Recording)</p>
        <div class="file-row">
          <span class="file-path">{videoPath || 'No file selected'}</span>
          <button class="btn btn--secondary" on:click={pickFile} disabled={uploading}>Browse…</button>
        </div>
      </div>

      <div class="action-row">
        <button class="btn btn--primary btn--wide" on:click={startUpload} disabled={uploading || testing || !videoPath}>
          {uploading ? 'Uploading…' : 'Start Upload'}
        </button>
        <button class="btn btn--ghost" on:click={runDryTest} disabled={uploading || testing} title="Test credentials without uploading anything">
          {testing ? 'Testing…' : 'Test Connections'}
        </button>
      </div>

      {#if finalResult}
        <div class="result-banner" class:result-banner--ok={finalResult.ok} class:result-banner--err={!finalResult.ok}>
          {finalResult.message}
        </div>
      {/if}

      <!-- Platform status cards -->
      <div class="cards">
        {#each platforms as p}
          <div class="card {stateClass(status[p].state)}">
            <span class="card__icon">{stateIcon(status[p].state)}</span>
            <div>
              <div class="card__label">{platformLabels[p]}</div>
              <div class="card__msg">{status[p].message}</div>
            </div>
          </div>
        {/each}
      </div>
    </section>

    <!-- ===== SETTINGS VIEW ===== -->
    {:else if view === 'settings'}
    <section class="panel">
      <h1 class="panel__title">Settings</h1>

      <h2 class="section-title">YouTube</h2>
      <p class="hint">Create OAuth credentials at <strong>console.cloud.google.com</strong> → APIs &amp; Services → Credentials → OAuth 2.0 Client ID (Desktop app). Add <code>http://localhost:9876/oauth2callback</code> as an Authorized redirect URI.</p>
      <label class="form__label" for="s-yt-id">Client ID</label>
      <input id="s-yt-id" class="form__input" type="text" bind:value={cfg.youtube_client_id} />
      <label class="form__label" for="s-yt-secret">Client Secret</label>
      <input id="s-yt-secret" class="form__input" type="password" bind:value={cfg.youtube_client_secret} />
      <label class="form__label" for="s-yt-privacy">Default Privacy</label>
      <select id="s-yt-privacy" class="form__input" bind:value={cfg.youtube_privacy}>
        <option value="unlisted">Unlisted</option>
        <option value="public">Public</option>
        <option value="private">Private</option>
      </select>
      <div class="auth-row">
        <span class="token-status">{hasYTToken ? '✓ Token stored' : '✗ Not authenticated'}</span>
        <button class="btn btn--secondary" on:click={authenticateYouTube}>Authenticate with Google</button>
      </div>
      {#if ytAuthStatus}<p class="hint">{ytAuthStatus}</p>{/if}

      <h2 class="section-title">Facebook</h2>
      <p class="hint">Generate a long-lived Page access token via <strong>developers.facebook.com → Graph API Explorer</strong>. Required permissions: <code>pages_manage_posts</code>, <code>pages_read_engagement</code>.</p>
      <label class="form__label" for="s-fb-page">Page ID</label>
      <input id="s-fb-page" class="form__input" type="text" bind:value={cfg.facebook_page_id} placeholder="e.g. 123456789" />
      <label class="form__label" for="s-fb-token">Page Access Token</label>
      <input id="s-fb-token" class="form__input" type="password" bind:value={cfg.facebook_access_token} />

      <h2 class="section-title">Spotify for Podcasters</h2>
      <p class="hint">The browser will open (visible) so you can handle 2FA manually if needed.</p>
      <label class="form__label" for="s-sp-email">Email</label>
      <input id="s-sp-email" class="form__input" type="email" bind:value={cfg.spotify_email} />
      <label class="form__label" for="s-sp-pass">Password</label>
      <input id="s-sp-pass" class="form__input" type="password" bind:value={cfg.spotify_password} />
      <label class="form__label" for="s-sp-url">Show URL</label>
      <input id="s-sp-url" class="form__input" type="text" bind:value={cfg.spotify_show_url} placeholder="https://podcasters.spotify.com/pod/show/your-show" />

      <h2 class="section-title">Output</h2>
      <label class="form__label" for="s-out-dir">Normalized file output directory (leave blank for system temp)</label>
      <input id="s-out-dir" class="form__input" type="text" bind:value={cfg.output_dir} placeholder="C:\Users\you\Videos\Sermons" />

      <button class="btn btn--primary" on:click={saveSettings}>Save Settings</button>
      {#if settingsSaved}<p class="hint hint--ok">Settings saved!</p>{/if}
      {#if settingsError}<p class="hint hint--err">{settingsError}</p>{/if}
    </section>
    {/if}

  </main>
</div>


