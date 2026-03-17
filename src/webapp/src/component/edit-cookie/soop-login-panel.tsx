import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Badge, Button, Checkbox, Divider, Input, notification, Space, Spin } from 'antd';
import API from '../../utils/api';
import './edit-cookie.css';

const { TextArea } = Input;

interface SoopLoginPanelProps {
    initialCookie: string;
    onCookieChange: (cookie: string) => void;
    onPersistStateChange: (persisted: boolean) => void;
    onAuthDraftChange: (draft: {
        username: string;
        password: string;
        saveCredentials: boolean;
        hasSavedCredentials: boolean;
        dirty: boolean;
        saveCredentialsTouched: boolean;
    }) => void;
    api: API;
}

const SoopLoginPanel: React.FC<SoopLoginPanelProps> = ({
    initialCookie,
    onCookieChange,
    onPersistStateChange,
    onAuthDraftChange,
    api
}) => {
    const [username, setUsername] = useState('');
    const [password, setPassword] = useState('');
    const [hasSavedCredentials, setHasSavedCredentials] = useState(false);
    const [saveCredentials, setSaveCredentials] = useState(true);
    const [textView, setTextView] = useState(initialCookie);
    const [loggingIn, setLoggingIn] = useState(false);
    const [verifying, setVerifying] = useState(false);
    const [verificationInfo, setVerificationInfo] = useState<any>(null);
    const [verificationError, setVerificationError] = useState('');
    const [cookieStatus, setCookieStatus] = useState<'missing' | 'valid' | 'invalid' | 'error'>('missing');
    const [clearing, setClearing] = useState(false);
    const [authDirty, setAuthDirty] = useState(false);
    const [saveCredentialsTouched, setSaveCredentialsTouched] = useState(false);

    const isMounted = useRef(true);
    const textViewRef = useRef(textView);

    textViewRef.current = textView;

    const hasValidVerification = cookieStatus === 'valid' && verificationInfo;
    const accountStatusText = cookieStatus === 'valid'
        ? '当前已检测到有效的 Soop 登录态，可直接用于录制。'
        : cookieStatus === 'invalid'
            ? '当前 Cookie 已失效，建议重新登录或手动更新 Cookie。'
            : cookieStatus === 'error'
                ? '当前无法确认 Cookie 状态，可稍后重试或重新登录。'
                : '推荐优先使用账号密码登录，程序会自动换取并写入 Cookie。';

    const verifyCookie = useCallback((cookie?: string) => {
        const target = cookie || textViewRef.current;
        if (!target) {
            return;
        }
        setVerifying(true);
        setVerificationInfo(null);
        setVerificationError('');
        api.verifySoopLiveCookie(target)
            .then((rsp: any) => {
                if (!isMounted.current) return;
                setVerifying(false);
                if (rsp?.err_no === 0 && rsp?.data?.isLogin) {
                    setVerificationInfo(rsp.data);
                    setCookieStatus('valid');
                } else {
                    setCookieStatus('invalid');
                    setVerificationError('Soop Cookie 校验未通过，当前登录态已失效或权限不足');
                    notification.warning({ message: 'Soop Cookie 可能无效或已过期' });
                }
            })
            .catch((err: any) => {
                if (!isMounted.current) return;
                setVerifying(false);
                setCookieStatus('error');
                setVerificationError(String(err));
                notification.error({ message: '验证失败', description: String(err) });
            });
    }, [api]);

    useEffect(() => {
        isMounted.current = true;
        api.getSoopLiveAuth()
            .then((rsp: any) => {
                if (!isMounted.current) return;
                if (rsp?.err_no === 0 && rsp?.data) {
                    setUsername(rsp.data.username || '');
                    setPassword('');
                    setHasSavedCredentials(Boolean(rsp.data.has_saved_credentials));
                    setSaveCredentials(Boolean(rsp.data.has_saved_credentials));
                    if (rsp.data.cookie_status === 'valid' && rsp.data.verify?.isLogin) {
                        setVerificationInfo(rsp.data.verify);
                    } else {
                        setVerificationInfo(null);
                    }
                    if (rsp.data.cookie_status) {
                        setCookieStatus(rsp.data.cookie_status);
                    }
                    if (rsp.data.verify_error) {
                        setVerificationError(rsp.data.verify_error);
                    }
                }
            })
            // 这里故意不阻断面板使用：
            // 即使初始化拉取失败，用户仍然可以手动输入账号密码或 Cookie 完成后续操作。
            .catch(() => undefined);

        return () => {
            isMounted.current = false;
        };
    }, [api]);

    useEffect(() => {
        onAuthDraftChange({
            username,
            password,
            saveCredentials,
            hasSavedCredentials,
            dirty: authDirty || saveCredentialsTouched,
            saveCredentialsTouched,
        });
    }, [authDirty, hasSavedCredentials, onAuthDraftChange, password, saveCredentials, saveCredentialsTouched, username]);

    const handleLogin = () => {
        if (!username || !password) {
            notification.warning({ message: '请输入账号和密码' });
            return;
        }

        setLoggingIn(true);
        api.loginSoopLive(username, password, saveCredentials)
            .then((rsp: any) => {
                if (!isMounted.current) return;
                setLoggingIn(false);
                if (rsp?.err_no === 0 && rsp?.data?.cookie) {
                    const cookie = rsp.data.cookie;
                    setTextView(cookie);
                    setHasSavedCredentials(saveCredentials);
                    setAuthDirty(false);
                    setSaveCredentialsTouched(false);
                    onCookieChange(cookie);
                    onPersistStateChange(true);
                    if (rsp.data.verify?.isLogin) {
                        setVerificationInfo(rsp.data.verify);
                        setCookieStatus('valid');
                        setVerificationError('');
                    } else {
                        verifyCookie(cookie);
                    }
                    notification.success({ message: 'Soop 登录成功，已完成 Cookie 写入并立即生效' });
                } else {
                    notification.error({ message: 'Soop 登录未返回有效 Cookie' });
                }
            })
            .catch((err: any) => {
                if (!isMounted.current) return;
                setLoggingIn(false);
                notification.error({ message: 'Soop 登录失败，未能完成账号密码换 Cookie', description: String(err) });
            });
    };

    const handleTextChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
        const value = e.target.value;
        setTextView(value);
        setVerificationInfo(null);
        setVerificationError('');
        setCookieStatus(value.trim() ? 'invalid' : 'missing');
        onCookieChange(value);
        onPersistStateChange(false);
    };

    const handleClear = () => {
        setClearing(true);
        api.clearSoopLiveAuth()
            .then(() => {
                if (!isMounted.current) return;
                setClearing(false);
                setUsername('');
                setPassword('');
                setHasSavedCredentials(false);
                setSaveCredentials(false);
                setAuthDirty(false);
                setSaveCredentialsTouched(false);
                setTextView('');
                setVerificationInfo(null);
                setVerificationError('');
                setCookieStatus('missing');
                onCookieChange('');
                onPersistStateChange(true);
                notification.success({ message: '已清空 Soop 账号密码与 Cookie' });
            })
            .catch((err: any) => {
                if (!isMounted.current) return;
                setClearing(false);
                notification.error({ message: '清空失败', description: String(err) });
            });
    };

    const passwordPlaceholder = hasSavedCredentials && !password
        ? '已保存密码，如需修改请重新输入'
        : '请输入 SoopLive 密码';

    return (
        <div className="bili-login-container">
            <div className="bili-login-layout">
                <div className="soop-account-section">
                    <div className="section-label" style={{ borderLeft: 'none', paddingLeft: 0 }}>
                        账号密码登录
                    </div>

                    <div className="login-msg-text soop-account-status">{accountStatusText}</div>

                    <Alert
                        style={{ marginBottom: 12, padding: '6px 10px' }}
                        showIcon
                        type="warning"
                        message="公网暴露 WebUI 可能泄露 Soop 账号密码，请仅在本机或可信内网中保存。"
                    />

                    <div className="soop-account-fields">
                        <Input
                            className="soop-account-input"
                            name="soop-login-username"
                            autoComplete="off"
                            placeholder="请输入 SoopLive 账号"
                            value={username}
                            onChange={(e) => {
                                setUsername(e.target.value);
                                setAuthDirty(true);
                            }}
                        />
                        <Input
                            className="soop-account-input"
                            name="soop-login-password"
                            autoComplete="new-password"
                            placeholder={passwordPlaceholder}
                            value={password}
                            onChange={(e) => {
                                setPassword(e.target.value);
                                setAuthDirty(true);
                            }}
                        />
                        <Checkbox
                            className="soop-account-checkbox"
                            checked={saveCredentials}
                            onChange={(e) => {
                                setSaveCredentials(e.target.checked);
                                setSaveCredentialsTouched(true);
                            }}
                        >
                            将账号密码保存到配置文件
                        </Checkbox>
                    </div>

                    <div className="soop-account-actions">
                        <Button danger ghost onClick={handleClear} loading={clearing}>
                            清空凭证
                        </Button>
                        <Button type="primary" onClick={handleLogin} loading={loggingIn}>
                            登录并写入 Cookie
                        </Button>
                    </div>

                    <div className="manual-tip soop-account-tip">
                        登录成功后，后端会返回 Cookie，并写入 `play.sooplive.co.kr` 的配置。
                    </div>
                </div>

                <div className="bili-manual-section">
                    <div className="section-label">
                        <span>手动管理 Cookie</span>
                        <Button
                            className="verify-btn"
                            size="small"
                            type="primary"
                            ghost
                            loading={verifying}
                            disabled={!textView}
                            onClick={() => verifyCookie(textView)}
                        >
                            验证 Cookie
                        </Button>
                    </div>
                    <TextArea
                        className="cookie-textarea"
                        placeholder="在此粘贴或修改 Soop Cookie...（手动修改后请点击上方按钮验证）"
                        value={textView}
                        autoSize={{ minRows: 6, maxRows: 6 }}
                        onChange={handleTextChange}
                    />
                    <div className="manual-tip">提示：如果登录接口不可用，可在浏览器登录 Soop 后手动复制 Cookie 到此处。</div>

                    {hasValidVerification ? (
                        <div className="verification-card">
                            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                                <div style={{ display: 'flex', alignItems: 'center' }}>
                                    <Badge status="processing" color="#52c41a" />
                                    <span className="user-badge">{verificationInfo.login_id || '已登录'}</span>
                                </div>
                            </div>
                            <div style={{ fontSize: '13px', color: '#52c41a', marginTop: '8px', fontWeight: 500 }}>
                                状态：Cookie 验证通过，可用于 SoopLive 登录态请求
                            </div>
                        </div>
                    ) : (
                        <div className="verification-card pending">
                            {verifying ? (
                                <Space><Spin size="small" /> 正在验证 Soop Cookie...</Space>
                            ) : (
                                <span>
                                    {verificationError
                                        ? `状态：${verificationError}`
                                        : cookieStatus === 'missing'
                                            ? '当前未保存 Soop Cookie，请先登录或手动输入 Cookie'
                                            : textView
                                                ? '请点击上方按钮验证 Cookie 有效性'
                                                : '请先登录或输入 Cookie'}
                                </span>
                            )}
                        </div>
                    )}
                </div>
            </div>

            <Divider className="divider-text">Soop 使用说明</Divider>

            <Alert
                className="info-alert"
                showIcon
                type="info"
                message={<span style={{ fontWeight: 700, fontSize: '15px' }}>Soop 登录说明</span>}
                description={
                    <div style={{ fontSize: '14px' }}>
                        <ul className="instruction-list">
                            <li>优先使用左侧账号密码登录，程序会自动换取 Cookie 并写入配置。</li>
                            <li>如登录接口失效，可在浏览器登录 Soop 后手动复制 Cookie 到右侧输入框。</li>
                            <li>面板会优先展示当前已保存 Cookie 的校验结果，便于判断是否需要重新登录。</li>
                        </ul>
                    </div>
                }
            />
        </div>
    );
};

export default SoopLoginPanel;
