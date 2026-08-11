import React from 'react';
import './App.css';
import { Routes, Route, useLocation } from 'react-router-dom';
import RootLayout from './component/layout/index';
import LiveList from './component/live-list/index';
import LiveInfo from './component/live-info/index';
import ConfigInfo from './component/config-info/index';
import FileList from './component/file-list/index';
import TaskPage from './component/task-page/index';
import IOStats from './component/io-stats/index';
import UpdateBanner from './component/update-banner/index';
import FFmpegBanner from './component/ffmpeg-banner/index';
import UpdatePage from './component/update-page/index';
import DanmakuSettings from './component/danmaku-config/index';
import DiagnosticViewer from './component/diagnostic-viewer/index';
import DiagnosticStartupBanner from './component/diagnostic-startup-banner/index';

const AppBanners: React.FC = () => {
  const location = useLocation();
  if (location.pathname.startsWith('/diagnostics')) {
    return null;
  }
  return (
    <>
      <DiagnosticStartupBanner />
      <FFmpegBanner />
      <UpdateBanner />
    </>
  );
};

const App: React.FC = () => {
  return (
    <RootLayout>
      <AppBanners />
        <Routes>
          <Route path="/update/*" element={<UpdatePage />} />
          <Route path="/iostats/*" element={<IOStats />} />
          <Route path="/tasks/*" element={<TaskPage />} />
          <Route path="/fileList/*" element={<FileList />} />
          <Route path="/danmaku" element={<DanmakuSettings />} />
          <Route path="/diagnostics/*" element={<DiagnosticViewer />} />
          <Route path="/configInfo/*" element={<ConfigInfo />} />
          <Route path="/liveInfo" element={<LiveInfo />} />
          <Route path="/" element={<LiveList />} />
        </Routes>
    </RootLayout>
  );
}

export default App;
