import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import './styles/tokens.css'
import './styles/global.css'
import { Layout } from './components'
import Overview from './pages/Overview'
import Sessions from './pages/Sessions'
import SessionDetail from './pages/SessionDetail'
import Costs from './pages/Costs'
import Tools from './pages/Tools'
import Models from './pages/Models'
import History from './pages/History'
import Users from './pages/Users'
import Setup from './pages/Setup'
createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter>
      <Layout>
        <Routes>
          <Route path="/" element={<Overview />} />
          <Route path="/sessions" element={<Sessions />} />
          <Route path="/sessions/:id" element={<SessionDetail />} />
          <Route path="/users" element={<Users />} />
          <Route path="/costs" element={<Costs />} />
          <Route path="/tools" element={<Tools />} />
          <Route path="/models" element={<Models />} />
          <Route path="/history" element={<History />} />
          <Route path="/settings" element={<Navigate to="/setup" replace />} />
          <Route path="/setup" element={<Setup />} />
        </Routes>
      </Layout>
    </BrowserRouter>
  </StrictMode>,
)
