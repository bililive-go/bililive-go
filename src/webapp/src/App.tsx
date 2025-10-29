import React from 'react';
import './App.css';
import 'antd/dist/antd.css';
import { Switch, Route } from 'react-router-dom';
import RootLayout from './component/layout/index';
import LiveList from './component/live-list/index';
import LiveInfo from './component/live-info/index';
import ConfigInfo from './component/config-info/index';
import FileList from './component/file-list/index';
import LogViewer from './component/log-viewer/index';

const App: React.FC = () => {
  return (
    <RootLayout>
      <Switch>
        <Route path="/fileList/:path(.*)?" component={FileList}></Route>
        <Route path="/configInfo" component={ConfigInfo}></Route>
        <Route path="/liveInfo" component={LiveInfo}></Route>
        <Route path="/logs" component={LogViewer}></Route>
        <Route path="/" component={LiveList}></Route>
      </Switch>
    </RootLayout>
  );
}

export default App;
