import React from 'react';
import './App.css';
import { Switch, Route } from 'react-router-dom';
import RootLayout from './component/layout/index';
import LiveList from './component/live-list/index';
import LiveInfo from './component/live-info/index';
import ConfigInfo from './component/config-info/index';
import FileList from './component/file-list/index';

const App: React.FC = () => {
  return (
    <RootLayout>
      {/* @ts-ignore */}
      <Switch>
        {/* @ts-ignore */}
        <Route path="/fileList/:path(.*)?" render={(props) => <FileList {...props} />} />
        {/* @ts-ignore */}
        <Route path="/configInfo" render={(props) => <ConfigInfo {...props} />} />
        {/* @ts-ignore */}
        <Route path="/liveInfo" render={(props) => <LiveInfo {...props} />} />
        {/* @ts-ignore */}
        <Route path="/" render={(props) => <LiveList {...props} />} />
      </Switch>
    </RootLayout>
  );
}

export default App;
