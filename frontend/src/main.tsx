import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Navigate, Route, Routes, useParams } from 'react-router-dom'
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
import Actors from './pages/Actors'
import ActorDetail from './pages/ActorDetail'
import Setup from './pages/Setup'
function LegacyUserRedirect() {
  const { id } = useParams()
  return <Navigate to={`/actors/${id ?? ''}`} replace />
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter>
      <Layout>
        <Routes>
          <Route path="/" element={<Overview />} />
          <Route path="/sessions" element={<Sessions />} />
          <Route path="/sessions/:id" element={<SessionDetail />} />
          <Route path="/actors" element={<Actors />} />
          <Route path="/actors/:id" element={<ActorDetail />} />
          {/* Pre-v11 URLs (ADR-0027): keep bookmarks to the old Users pages working. */}
          <Route path="/users" element={<Navigate to="/actors" replace />} />
          <Route path="/users/:id" element={<LegacyUserRedirect />} />
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
