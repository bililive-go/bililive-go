import React from "react";
import Editor from 'react-simple-code-editor';
import { highlight, languages } from 'prismjs';
import 'prismjs/components/prism-yaml';
import 'prismjs/components/prism-clike';
import 'prismjs/components/prism-javascript';
import 'prismjs/themes/prism.css'; //Example style, you can use another
import API from '../../utils/api';
import { Button } from "antd";
import './config-info.css';
const api = new API();

interface Props {

}

interface IState {
  config: any
}

class ConfigInfo extends React.Component<Props, IState> {

  constructor(props: Props) {
    super(props);
    this.state = {
      config: null,
    }
  }

  componentDidMount(): void {
    api.getConfigInfo()
      .then((rsp: any) => {
        this.setState({
          config: rsp.config
        });
      })
      .catch(err => {
        alert("获取配置信息失败");
      });
  }

  /**
     * 保存设置至config文件
     */
  onSettingSave = () => {
    api.saveRawConfig({ config: this.state.config })
      .then((rsp: any) => {
        if (rsp.err_no === 0) {
          alert("设置保存成功");
        } else {
          alert(`Server Error!\n${rsp.err_msg}`);
        }
      })
      .catch(err => {
        alert("设置保存失败！");
      })
  }

  render() {
    if (this.state.config === null) {
      return <div>loading...</div>;
    }
    return <div>
      <Editor
        value={this.state.config}
        onValueChange={code => this.setState({ config: code })}
        highlight={code => {
          return highlight(code, languages.yaml, 'yaml');
        }}
        padding={10}
        style={{
          fontFamily: '"Fira code", "Fira Mono", monospace',
          fontSize: 12,
        }}
      />
      <Button
        type="default"
        style={{
          marginTop: 16,
        }}
        onClick={this.onSettingSave}
      >
        保存设置
      </Button>
    </div>
  }
}

export default ConfigInfo;