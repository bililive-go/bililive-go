import React, { useState, useEffect, useRef, useCallback } from 'react';
import { Button, Spin, Input, Badge, Alert, Divider, notification, Space } from 'antd';
import API from '../../utils/api';
import { buildCookieString, parseCookieString } from '../../utils/cookie';
import './edit-cookie.css';

const { TextArea } = Input;

const BILI_COOKIE_FIELDS = ['SESSDATA', 'bili_jct', 'DedeUserID', 'DedeUserID__ckMd5', 'sid'] as const;

interface BiliLoginPanelProps {
    initialCookie: string;
    onCookieChange: (cookie: string) => void;
    api: API;
}

const BiliLoginPanel: React.FC<BiliLoginPanelProps> = ({ initialCookie, onCookieChange, api }) => {
    const [qrCodeUrl, setQrCodeUrl] = useState('');
    const [loginStatus, setLoginStatus] = useState<'loading' | 'active' | 'scanned' | 'expired' | 'success'>('loading');
    const [loginMsg, setLoginMsg] = useState('正在获取二维码...');
    const [textView, setTextView] = useState(initialCookie);
    const [verifying, setVerifying] = useState(false);
    const [verificationInfo, setVerificationInfo] = useState<any>(null);

    const pollTimerRef = useRef<any>(null);
    const hasAutoVerified = useRef(false);
    const isMounted = useRef(true);
    const textViewRef = useRef(textView);
    const onCookieChangeRef = useRef(onCookieChange);

    // Keep refs updated for stable callbacks
    textViewRef.current = textView;
    onCookieChangeRef.current = onCookieChange;

    const verifyCookie = useCallback((cookie?: string) => {
        const target = cookie || textViewRef.current;
        if (!target) return;
        setVerifying(true);
        setVerificationInfo(null);
        api.verifyBilibiliCookie(target)
            .then((rsp: any) => {
                if (!isMounted.current) return;
                setVerifying(false);
                if (rsp.code === 0 && rsp.data && rsp.data.isLogin) {
                    setVerificationInfo({
                        uname: rsp.data.uname,
                        mid: rsp.data.mid,
                        level: rsp.data.level_info?.current_level
                    });
                } else {
                    notification.warning({ message: 'Cookie 可能无效或已过期' });
                }
            })
            .catch(() => {
                if (!isMounted.current) return;
                setVerifying(false);
            });
    }, [api]);

    const processLoginSuccess = useCallback((urlStr: string) => {
        try {
            const urlObj = new URL(urlStr);
            const params = urlObj.searchParams;
            const cookies = [
                `DedeUserID=${params.get('DedeUserID')}`,
                `DedeUserID__ckMd5=${params.get('DedeUserID__ckMd5')}`,
                `SESSDATA=${params.get('SESSDATA')}`,
                `bili_jct=${params.get('bili_jct')}`,
                `sid=${params.get('sid')}`,
            ];
            // Filter out null values and join
            const cookieStr = cookies.filter(c => !c.includes('null')).join('; ') + ';';
            setTextView(cookieStr);
            onCookieChangeRef.current(cookieStr);
            // Auto verify after success
            verifyCookie(cookieStr);
        } catch (e) {
            console.error(e);
            notification.error({ message: '解析结果失败' });
        }
    }, [verifyCookie]);

    const startPolling = useCallback((key: string) => {
        if (pollTimerRef.current) clearInterval(pollTimerRef.current);
        pollTimerRef.current = setInterval(() => {
            api.pollBilibiliQRCode(key)
                .then((res: any) => {
                    if (!isMounted.current) return;
                    if (res.code === 0) {
                        const data = res.data;
                        if (data.code === 0) {
                            clearInterval(pollTimerRef.current);
                            setLoginStatus('success');
                            setLoginMsg('登录成功！');
                            processLoginSuccess(data.url);
                        } else if (data.code === 86101) {
                            setLoginStatus('active');
                            setLoginMsg('等待扫描...');
                        } else if (data.code === 86090) {
                            setLoginStatus('scanned');
                            setLoginMsg('请在手机上确认登录');
                        } else if (data.code === 86038) {
                            setLoginStatus('expired');
                            setLoginMsg('二维码已过期，请关闭当前界面并重新打开以获取新二维码');
                            clearInterval(pollTimerRef.current);
                        }
                    }
                })
                .catch(console.error);
        }, 2000);
    }, [api, processLoginSuccess]);

    const getBiliQRCode = useCallback(() => {
        setLoginStatus('loading');
        setLoginMsg('正在获取二维码...');
        api.getBilibiliQRCode()
            .then((res: any) => {
                if (!isMounted.current) return;
                if (res.code === 0) {
                    setQrCodeUrl(res.data.url);
                    setLoginStatus('active');
                    setLoginMsg('请扫描此二维码');
                    startPolling(res.data.qrcode_key);
                } else {
                    setLoginStatus('expired');
                    setLoginMsg('获取二维码失败: ' + res.message);
                }
            })
            .catch(err => {
                if (!isMounted.current) return;
                setLoginStatus('expired');
                setLoginMsg('获取连接失败');
                console.error(err);
            });
    }, [api, startPolling]);

    const handleTextChange = (e: any) => {
        const val = e.target.value;
        setTextView(val);
        onCookieChange(val);
    };

    const cookieMap = parseCookieString(textView);

    const handleStructuredCookieChange = (key: typeof BILI_COOKIE_FIELDS[number], value: string) => {
        const nextCookieMap = parseCookieString(textViewRef.current);
        const trimmedValue = value.trim();
        if (trimmedValue) {
            nextCookieMap[key] = trimmedValue;
        } else {
            delete nextCookieMap[key];
        }
        const nextCookie = buildCookieString(nextCookieMap, BILI_COOKIE_FIELDS);
        setTextView(nextCookie);
        onCookieChange(nextCookie);
        setVerificationInfo(null);
    };

    useEffect(() => {
        isMounted.current = true;
        getBiliQRCode();
        return () => {
            isMounted.current = false;
            if (pollTimerRef.current) clearInterval(pollTimerRef.current);
        };
    }, [getBiliQRCode]);

    // 仅在打开界面且初始有 Cookie 时自动验证一次
    useEffect(() => {
        if (initialCookie && !hasAutoVerified.current) {
            verifyCookie(initialCookie);
            hasAutoVerified.current = true;
        }
    }, [initialCookie, verifyCookie]);

    return (
        <div className="bili-login-container">
            <div className="bili-login-layout">
                {/* QR Section */}
                <div className="bili-qr-section">
                    <div className="section-label" style={{ borderLeft: 'none', paddingLeft: 0, justifyContent: 'center' }}>
                        扫码快速登录
                    </div>
                    <div className="qr-frame">
                        {loginStatus === 'loading' ? (
                            <div className="qr-overlay"><Spin tip="获取中..." /></div>
                        ) : (
                            <>
                                <img
                                    className="qr-image"
                                    src={`https://api.qrserver.com/v1/create-qr-code/?size=160x160&data=${encodeURIComponent(qrCodeUrl)}`}
                                    alt="QR Code"
                                />
                                {(loginStatus === 'scanned' || loginStatus === 'success' || loginStatus === 'expired') && (
                                    <div className="qr-overlay">
                                        <div className="qr-status-icon">
                                            {loginStatus === 'scanned' && '📱'}
                                            {loginStatus === 'success' && '✅'}
                                            {loginStatus === 'expired' && '⌛'}
                                        </div>
                                        <div className="qr-status-text">
                                            {loginStatus === 'scanned' && '已扫描，待确认'}
                                            {loginStatus === 'success' && '登录成功'}
                                            {loginStatus === 'expired' && '二维码已过期'}
                                        </div>
                                        {loginStatus === 'expired' && (
                                            <div style={{ color: '#fff', fontSize: '12px', marginTop: 10, padding: '0 10px', textAlign: 'center' }}>
                                                请关闭管理界面并重新打开以刷新二维码
                                            </div>
                                        )}
                                    </div>
                                )}
                            </>
                        )}
                    </div>
                    <div className="login-msg-text">{loginMsg}</div>
                </div>

                {/* Manual Section */}
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
                            重新验证
                        </Button>
                    </div>
                    <Alert
                        className="cookie-highlight-alert"
                        showIcon
                        type="warning"
                        message="推荐优先填写下面 5 个关键 Cookie 字段"
                        description="对哔哩哔哩录制来说，最常用且最关键的是 SESSDATA、bili_jct、DedeUserID、DedeUserID__ckMd5、sid。请尽量逐项填写，避免整段 Cookie 中夹杂无关内容。"
                    />
                    <div className="cookie-field-grid">
                        <div className="cookie-field-item cookie-field-item-full">
                            <div className="cookie-field-label">SESSDATA</div>
                            <Input value={cookieMap.SESSDATA || ''} placeholder="必填，登录态核心字段" onChange={(e) => handleStructuredCookieChange('SESSDATA', e.target.value)} />
                        </div>
                        <div className="cookie-field-item">
                            <div className="cookie-field-label">bili_jct</div>
                            <Input value={cookieMap.bili_jct || ''} placeholder="必填，CSRF 相关字段" onChange={(e) => handleStructuredCookieChange('bili_jct', e.target.value)} />
                        </div>
                        <div className="cookie-field-item">
                            <div className="cookie-field-label">DedeUserID</div>
                            <Input value={cookieMap.DedeUserID || ''} placeholder="必填，用户 ID" onChange={(e) => handleStructuredCookieChange('DedeUserID', e.target.value)} />
                        </div>
                        <div className="cookie-field-item">
                            <div className="cookie-field-label">DedeUserID__ckMd5</div>
                            <Input value={cookieMap.DedeUserID__ckMd5 || ''} placeholder="建议填写" onChange={(e) => handleStructuredCookieChange('DedeUserID__ckMd5', e.target.value)} />
                        </div>
                        <div className="cookie-field-item">
                            <div className="cookie-field-label">sid</div>
                            <Input value={cookieMap.sid || ''} placeholder="建议填写" onChange={(e) => handleStructuredCookieChange('sid', e.target.value)} />
                        </div>
                    </div>
                    <Divider className="divider-text">原始 Cookie 字符串（高级编辑）</Divider>
                    <TextArea
                        className="cookie-textarea"
                        placeholder="可直接粘贴完整 Cookie 字符串；上方字段会优先帮助你填写关键项"
                        value={textView}
                        autoSize={{ minRows: 6, maxRows: 6 }}
                        onChange={handleTextChange}
                    />
                    <div className="manual-tip">提示：无论是逐项填写还是粘贴整段 Cookie，保存前都建议点击右上方“重新验证”确认有效性。</div>

                    {verificationInfo ? (
                        <div className="verification-card">
                            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                                <div style={{ display: 'flex', alignItems: 'center' }}>
                                    <Badge status="processing" color="#52c41a" />
                                    <span className="user-badge">{verificationInfo.uname}</span>
                                    <span className="uid-text">UID: {verificationInfo.mid}</span>
                                </div>
                                {verificationInfo.level !== undefined && (
                                    <Badge
                                        count={`Lv${verificationInfo.level}`}
                                        style={{ backgroundColor: '#faad14', color: '#fff', fontSize: '13px', padding: '0 8px', borderRadius: '4px' }}
                                    />
                                )}
                            </div>
                            <div style={{ fontSize: '13px', color: '#52c41a', marginTop: '8px', fontWeight: 500 }}>
                                状态：Cookie 验证通过，可正常抓取原画流
                            </div>
                        </div>
                    ) : (
                        <div className="verification-card pending">
                            {verifying ? (
                                <Space><Spin size="small" /> 正在验证 Cookie 有效性...</Space>
                            ) : (
                                <span>{textView ? '请点击上方按钮验证 Cookie 有效性' : '请扫码或输入 Cookie 以开始验证'}</span>
                            )}
                        </div>
                    )}
                </div>
            </div>

            <Divider className="divider-text">如果您选择手动获取 Cookie</Divider>

            <Alert
                className="info-alert"
                showIcon
                message={<span style={{ fontWeight: 700, fontSize: '15px' }}>手动获取教程</span>}
                type="info"
                description={
                    <div style={{ fontSize: '14px' }}>
                        推荐使用扫码登录，如录制画质受限（4K）-触发风控-弹幕获取失败等。请手动获取 Cookie，步骤如下：
                        <ul className="instruction-list">
                            <li>在浏览器打开 <b>哔哩哔哩</b> 并保持登录状态。</li>
                            <li>按键盘上的 <b>F12</b> 或右键选择 <b>检查</b>，切换到 <b>网络 (Network)</b> 面板。</li>
                            <li>刷新页面，点开列表中的 <b>www.bilibili.com</b> 第一个请求，在 <b>标头 (Headers)</b> 中找到 <b>Cookie</b> 一栏。</li>
                            <li><b>右键选中复制值</b>，并粘贴到上方输入框内。</li>
                        </ul>
                    </div>
                }
            />
        </div>
    );
};

export default BiliLoginPanel;
