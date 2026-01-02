import React from 'react';
import './App.css';
import { Routes, Route } from 'react-router-dom';
import RootLayout from './component/layout/index';
import LiveList from './component/live-list/index';
import LiveInfo from './component/live-info/index';
import ConfigInfo from './component/config-info/index';
import FileList from './component/file-list/index';

const App: React.FC = () => {
  return (
    <RootLayout>
      <Routes>
        <Route path="/fileList/*" element={<FileList />} />
        <Route path="/configInfo/*" element={<ConfigInfo />} />
        <Route path="/liveInfo" element={<LiveInfo />} />
        <Route path="/" element={<LiveList />} />
      </Routes>
    </RootLayout>
  );
}

export default App;
